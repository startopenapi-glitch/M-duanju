package service

import (
	"drama-platform/internal/model"
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrProjectFilter = errors.New("项目筛选条件无效")

type ProjectListFilter struct{ Query, Status string }

func (f ProjectListFilter) validate() error {
	if utf8.RuneCountInString(strings.TrimSpace(f.Query)) > 100 {
		return ErrProjectFilter
	}
	switch f.Status {
	case "", "DRAFT", "SCRIPT_GENERATING", "SCRIPT_REVIEW", "KEYVISUAL_GENERATING", "KEYVISUAL_REVIEW", "SHOTS_GENERATING", "SHOTS_REVIEW", "ASSEMBLING", "COMPLETED", "FAILED", "BUDGET_EXCEEDED":
		return nil
	}
	return ErrProjectFilter
}
func (s *Service) ListProjectsFiltered(userID string, page, pageSize int, filter ProjectListFilter) (*model.PageResp, error) {
	if err := filter.validate(); err != nil {
		return nil, err
	}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(filter.Query)) + "%"
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var total int
	if err := s.db.QueryRow(`SELECT count(*) FROM projects WHERE user_id=$1 AND deleted_at IS NULL AND (title ILIKE $2 OR idea ILIKE $2) AND ($3='' OR status=$3)`, userID, pattern, filter.Status).Scan(&total); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT id,user_id,title,idea,aspect,total_seconds,status,workflow_mode,budget_credits,spent_credits,
		       COALESCE(final_url,''),created_at,updated_at
		FROM projects WHERE user_id=$1 AND deleted_at IS NULL AND (title ILIKE $2 OR idea ILIKE $2) AND ($3='' OR status=$3) ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`,
		userID, pattern, filter.Status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*model.Project{}
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.UserID, &p.Title, &p.Idea, &p.Aspect, &p.TotalSeconds,
			&p.Status, &p.WorkflowMode, &p.BudgetCredits, &p.SpentCredits, &p.FinalURL, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &model.PageResp{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}
