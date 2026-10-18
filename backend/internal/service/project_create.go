package service

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"drama-platform/internal/model"
)

var ErrProjectCreationConflict = errors.New("原创建请求不匹配或项目已删除，请勿重复提交")
var ErrProjectCreationInvalid = errors.New("创建参数无效：时长须为 5-180 秒的 5 的倍数，请求键最多 64 字符")

func (s *Service) CreateProject(userID string, req *model.CreateProjectReq) (*model.Project, error) {
	if req == nil || req.TotalSeconds%5 != 0 || req.TotalSeconds < 5 || req.TotalSeconds > 180 || len(req.IdempotencyKey) > 64 {
		return nil, ErrProjectCreationInvalid
	}
	styleJSON, _ := json.Marshal(map[string]string{"film_reference": req.FilmRef, "mood": req.Mood})
	estimate, e := EstimateProject(req.TotalSeconds)
	if e != nil {
		return nil, e
	}
	budgetCredits := estimate.EstimatedCredits * 2
	if budgetCredits < 300 {
		budgetCredits = 300
	}
	// Hash only creation intent, not ignored client budget or changing server rates.
	input, _ := json.Marshal([]any{req.Title, req.Idea, req.Aspect, req.TotalSeconds, req.FilmRef, req.Mood})
	digest := sha256.Sum256(input)
	hash := hex.EncodeToString(digest[:])
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if req.IdempotencyKey != "" {
		// Exact account/request identity serializes both first creation and replay.
		if _, e = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "project-create:"+userID+":"+req.IdempotencyKey); e != nil {
			return nil, e
		}
		var id sql.NullString
		var oldHash string
		e = tx.QueryRow(`SELECT project_id,input_hash FROM project_creation_requests WHERE user_id=$1 AND request_key=$2`, userID, req.IdempotencyKey).Scan(&id, &oldHash)
		if e == nil {
			if oldHash != hash || !id.Valid {
				return nil, ErrProjectCreationConflict
			}
			p, e := readCreatedProject(tx, id.String, userID)
			if errors.Is(e, sql.ErrNoRows) {
				return nil, ErrProjectCreationConflict
			}
			if e != nil {
				return nil, e
			}
			return p, tx.Commit()
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	var p model.Project
	e = tx.QueryRow(`INSERT INTO projects (user_id,title,idea,aspect,total_seconds,budget_credits,style_config)
  VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
  RETURNING id,user_id,title,idea,aspect,total_seconds,status,budget_credits,spent_credits,created_at,updated_at`,
		userID, req.Title, req.Idea, req.Aspect, req.TotalSeconds, budgetCredits, string(styleJSON)).Scan(
		&p.ID, &p.UserID, &p.Title, &p.Idea, &p.Aspect, &p.TotalSeconds, &p.Status, &p.BudgetCredits, &p.SpentCredits, &p.CreatedAt, &p.UpdatedAt)
	if e != nil {
		return nil, e
	}
	if req.IdempotencyKey != "" {
		if _, e = tx.Exec(`INSERT INTO project_creation_requests(user_id,request_key,input_hash,project_id) VALUES($1,$2,$3,$4)`, userID, req.IdempotencyKey, hash, p.ID); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, fmt.Errorf("creation persistence unconfirmed: %w", e)
	}
	s.Log(userID, p.ID, "", "create_project", "", "DRAFT", req.Idea)
	return &p, nil
}

type projectRowReader interface{ QueryRow(string, ...any) *sql.Row }

func readCreatedProject(db projectRowReader, id, user string) (*model.Project, error) {
	var p model.Project
	e := db.QueryRow(`SELECT id,user_id,title,idea,aspect,total_seconds,status,budget_credits,spent_credits,created_at,updated_at FROM projects WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, user).Scan(&p.ID, &p.UserID, &p.Title, &p.Idea, &p.Aspect, &p.TotalSeconds, &p.Status, &p.BudgetCredits, &p.SpentCredits, &p.CreatedAt, &p.UpdatedAt)
	return &p, e
}

// CreationContext is read-only and strictly scoped to the authenticated user.
func (s *Service) CreationContext(user, key string) (*model.Project, error) {
	if key == "" || len(key) > 64 {
		return nil, ErrProjectCreationInvalid
	}
	var id sql.NullString
	e := s.db.QueryRow(`SELECT project_id FROM project_creation_requests WHERE user_id=$1 AND request_key=$2`, user, key).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !id.Valid {
		return nil, ErrProjectCreationConflict
	}
	p, e := readCreatedProject(s.db, id.String, user)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrProjectCreationConflict
	}
	return p, e
}
