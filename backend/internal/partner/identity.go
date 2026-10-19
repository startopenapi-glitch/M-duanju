package partner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func newID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// 推广码：安全随机、≥12 位；去掉易混字符，避免人工抄录出错
const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
const codeLen = 16

func NewCode() (string, error) {
	buf := make([]byte, codeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, codeLen)
	for i, b := range buf {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "23505") || strings.Contains(s, "duplicate key")
}

// Create 开通合作方。管理员角色与代理身份分离：调用方负责确认 user 存在，这里不授予任何管理员权限。
func (s *Store) Create(ctx context.Context, actorID, userID, name, intro, wechat string) (*Partner, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("合作方名称不能为空")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM partners WHERE user_id=$1)`, userID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrAlreadyExists
	}

	partnerID := newID()
	var code string
	inserted := false
	for attempt := 0; attempt < 5 && !inserted; attempt++ {
		if code, err = NewCode(); err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO partners(id,user_id,name,intro,wechat,code,status)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`, partnerID, userID, name, intro, wechat, code, StatusActive)
		if err == nil {
			inserted = true
			break
		}
		if !isUniqueViolation(err) {
			return nil, err
		}
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM partners WHERE user_id=$1)`, userID).Scan(&exists); e != nil {
			return nil, e
		}
		if exists {
			return nil, ErrAlreadyExists
		}
	}
	if !inserted {
		return nil, ErrCodeTaken
	}
	if err = s.audit(ctx, tx, partnerID, actorID, "create", map[string]any{"userId": userID, "name": name}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.ByID(ctx, partnerID)
}

// SetStatus 暂停/恢复。带版本校验，避免并发覆盖。
func (s *Store) SetStatus(ctx context.Context, actorID, id, status string, version int64) (*Partner, error) {
	if !ValidStatus(status) {
		return nil, errors.New("状态只能是 active 或 suspended")
	}
	return s.mutate(ctx, actorID, id, version, "status:"+status,
		`UPDATE partners SET status=$2, version=version+1, updated_at=now() WHERE id=$1 AND version=$3`,
		status, version)
}

func (s *Store) UpdateProfile(ctx context.Context, actorID, id, name, intro, wechat string, version int64) (*Partner, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("合作方名称不能为空")
	}
	if len([]rune(intro)) > 80 {
		return nil, errors.New("简介最多 80 字")
	}
	return s.mutate(ctx, actorID, id, version, "profile",
		`UPDATE partners SET name=$2,intro=$3,wechat=$4,version=version+1,updated_at=now() WHERE id=$1 AND version=$5`,
		name, intro, wechat, version)
}

func (s *Store) mutate(ctx context.Context, actorID, id string, version int64, action, query string, args ...any) (*Partner, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, append([]any{id}, args...)...)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		var cur int64
		if e := tx.QueryRowContext(ctx, `SELECT version FROM partners WHERE id=$1`, id).Scan(&cur); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, e
		}
		return nil, ErrVersionConflict
	}
	if err = s.audit(ctx, tx, id, actorID, action, map[string]any{"version": version}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.ByID(ctx, id)
}

func (s *Store) audit(ctx context.Context, tx *sql.Tx, partnerID, actorID, action string, detail map[string]any) error {
	payload := "{}"
	if len(detail) > 0 {
		parts := make([]string, 0, len(detail))
		for k, v := range detail {
			parts = append(parts, fmt.Sprintf("%q:%q", k, fmt.Sprint(v)))
		}
		payload = "{" + strings.Join(parts, ",") + "}"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO partner_audit_events(id,partner_id,actor_id,action,detail)
	 VALUES($1,$2,$3,$4,$5::jsonb)`, newID(), partnerID, actorID, action, payload)
	return err
}

const partnerCols = `id,user_id,name,intro,wechat,code,status,version,created_at,updated_at`

func scanPartner(row interface{ Scan(...any) error }) (*Partner, error) {
	var p Partner
	var created, updated time.Time
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Intro, &p.Wechat, &p.Code, &p.Status, &p.Version, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = created.UTC().Format(time.RFC3339)
	p.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return &p, nil
}

func (s *Store) ByID(ctx context.Context, id string) (*Partner, error) {
	return scanPartner(s.db.QueryRowContext(ctx, `SELECT `+partnerCols+` FROM partners WHERE id=$1`, id))
}

func (s *Store) ByUser(ctx context.Context, userID string) (*Partner, error) {
	return scanPartner(s.db.QueryRowContext(ctx, `SELECT `+partnerCols+` FROM partners WHERE user_id=$1`, userID))
}

func (s *Store) ByCode(ctx context.Context, code string) (*Partner, error) {
	return scanPartner(s.db.QueryRowContext(ctx, `SELECT `+partnerCols+` FROM partners WHERE code=$1`, code))
}

// PublicProfile 只暴露推广页允许的字段；暂停的推广码返回不可用原因，但不阻断注册流程。
func (s *Store) PublicProfile(ctx context.Context, code string) (*PublicProfile, error) {
	p, err := s.ByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	out := &PublicProfile{Code: p.Code, Name: p.Name, Intro: p.Intro, Wechat: p.Wechat, Usable: true}
	if p.Status != StatusActive {
		out.Usable = false
		out.Reason = "该推广链接已暂停，你仍可正常注册使用平台"
	}
	return out, nil
}
