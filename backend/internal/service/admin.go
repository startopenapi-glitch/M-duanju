package service

import (
	"database/sql"
	"fmt"
	"time"

	"drama-platform/internal/model"
)

// ══════════════════ 项目状态 ══════════════════

func (s *Service) SetProjectStatus(id, status string) {
	s.db.Exec(`UPDATE projects SET status=$1, updated_at=now() WHERE id=$2`, status, id)
}

func (s *Service) SetProjectError(id, msg string) {
	s.db.Exec(`UPDATE projects SET status='FAILED', error_msg=$1, updated_at=now() WHERE id=$2`, msg, id)
}

// ══════════════════ 管理后台 ══════════════════

func (s *Service) AdminStats() map[string]interface{} {
	out := map[string]interface{}{}

	var users, projects, completed, orders, pending int
	var credits int64
	s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&users)
	s.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&projects)
	s.db.QueryRow(`SELECT count(*) FROM projects WHERE status='COMPLETED'`).Scan(&completed)
	s.db.QueryRow(`SELECT count(*) FROM recharge_orders WHERE status='pending'`).Scan(&pending)
	s.db.QueryRow(`SELECT count(*) FROM recharge_orders WHERE status='paid'`).Scan(&orders)
	s.db.QueryRow(`SELECT COALESCE(sum(credits),0) FROM users`).Scan(&credits)

	var recharged float64
	s.db.QueryRow(`SELECT COALESCE(sum(amount_yuan),0) FROM recharge_orders WHERE status='paid'`).Scan(&recharged)

	out["users"] = users
	out["projects"] = projects
	out["completed"] = completed
	out["paidOrders"] = orders
	out["pendingOrders"] = pending
	out["totalCredits"] = credits
	out["totalRechargedYuan"] = recharged
	return out
}

func (s *Service) ListUsers(kw string, page, pageSize int) (*model.PageResp, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	where := ""
	args := []interface{}{}
	if kw != "" {
		where = " WHERE email ILIKE $1 OR nickname ILIKE $1"
		args = append(args, "%"+kw+"%")
	}

	var total int
	if err := s.db.QueryRow(`SELECT count(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	q := `SELECT id,email,COALESCE(nickname,''),role,credits,status,created_at FROM users` + where +
		fmt.Sprintf(` ORDER BY created_at DESC,id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*model.User{}
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Nickname, &u.Role, &u.Credits, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &u)
	}
	return &model.PageResp{Items: items, Total: total, Page: page, PageSize: pageSize}, rows.Err()
}

var _ = sql.ErrNoRows
var _ = time.Now
