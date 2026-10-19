package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"drama-platform/internal/model"
	runstore "drama-platform/internal/run"
)

// These projections are read-only. Every source error is returned to the caller.
func adminPaging(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

type adminScanner interface{ Scan(...any) error }

func (s *Service) AdminOverview() (map[string]any, error) {
	var users, projects, completed, pending, recharged, credits, active, unknown, failed, refunds int64
	var asOf time.Time
	err := s.db.QueryRow(`SELECT
 (SELECT count(*) FROM users),
 (SELECT count(*) FROM projects),
 (SELECT count(*) FROM projects WHERE status='COMPLETED'),
 (SELECT count(*) FROM recharge_orders WHERE status='pending'),
 (SELECT COALESCE(sum(amount_cents),0) FROM recharge_orders WHERE status='paid' AND NOT mock),
 (SELECT COALESCE(sum(credits),0) FROM users),
 (SELECT count(*) FROM generation_runs WHERE status IN ('queued','running','waiting_credits','settlement_pending')),
 (SELECT count(*) FROM jobs WHERE error_code='submission_unknown' OR (status='reconcile' AND phase='submitting')),
 (SELECT count(*) FROM generation_runs WHERE status='failed'),
 (SELECT count(*) FROM refund_intents WHERE status IN ('submitting','processing','unknown')),now()`).Scan(
		&users, &projects, &completed, &pending, &recharged, &credits, &active, &unknown, &failed, &refunds, &asOf)
	if err != nil {
		return nil, err
	}
	return map[string]any{"users": users, "projects": projects, "completed": completed, "pendingOrders": pending, "totalRechargedCents": recharged, "totalCredits": credits, "activeRuns": active, "unknownJobs": unknown, "failedRuns": failed, "refundPending": refunds, "asOf": asOf.UTC().Format(time.RFC3339)}, nil
}

const adminOrderColumns = `o.id,o.user_id,o.amount_yuan,o.amount_cents,o.credits,o.status,
 COALESCE(o.channel,''),COALESCE(o.pay_mode,'manual'),COALESCE(o.admin_note,''),
 COALESCE(o.out_trade_no,''),COALESCE(o.code_url,''),COALESCE(o.h5_url,''),
 COALESCE(o.transaction_id,''),o.credited,o.mock,o.created_at,
 o.refund_amount_cents,COALESCE(o.refund_reason,''),o.refunded_at,COALESCE(o.refund_no,''),
 COALESCE(o.run_id,''),COALESCE(o.external_id,''),COALESCE(o.handled_by,''),o.handled_at,o.paid_at,u.email`

func scanAdminOrder(row adminScanner) (*model.RechargeOrder, error) {
	var o model.RechargeOrder
	var handled, paid sql.NullTime
	var email string
	err := row.Scan(&o.ID, &o.UserID, &o.AmountYuan, &o.AmountCents, &o.Credits, &o.Status, &o.Channel, &o.PayMode, &o.AdminNote,
		&o.OutTradeNo, &o.CodeURL, &o.H5URL, &o.TransactionID, &o.Credited, &o.Mock, &o.CreatedAt, &o.RefundAmountCents, &o.RefundReason, &o.RefundedAt, &o.RefundNo, &o.RunID, &o.ExternalID, &o.HandledBy, &handled, &paid, &email)
	if err != nil {
		return nil, err
	}
	o.HandledAt = handled.Time
	o.PaidAt = paid.Time
	o.User = &model.User{ID: o.UserID, Email: email}
	return &o, nil
}

func (s *Service) AdminOrders(kw, status string, page, size int) (*model.PageResp, error) {
	page, size = adminPaging(page, size)
	where := ` FROM recharge_orders o JOIN users u ON u.id=o.user_id WHERE ($1='' OR o.id ILIKE $2 OR u.email ILIKE $2 OR COALESCE(o.out_trade_no,'') ILIKE $2) AND ($3='' OR o.status=$3)`
	args := []any{strings.TrimSpace(kw), "%" + strings.TrimSpace(kw) + "%", status}
	var total int
	if err := s.db.QueryRow(`SELECT count(*)`+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+adminOrderColumns+where+` ORDER BY o.created_at DESC,o.id DESC LIMIT $4 OFFSET $5`, append(args, size, int64(page-1)*int64(size))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.RechargeOrder{}
	for rows.Next() {
		o, e := scanAdminOrder(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &model.PageResp{Items: out, Total: total, Page: page, PageSize: size}, nil
}

func (s *Service) adminLinkedRun(id, user, project string) (*runstore.Run, error) {
	if id == "" {
		return nil, nil
	}
	r, e := s.runstore.ByID(context.Background(), id)
	if e != nil {
		return nil, e
	}
	if r.UserID != user || (project != "" && r.ProjectID != project) {
		return nil, errors.New("linked execution ownership mismatch")
	}
	// An order's run must also still belong to a project owned by the same user.
	var owner string
	if e = s.db.QueryRow(`SELECT user_id FROM projects WHERE id=$1`, r.ProjectID).Scan(&owner); e != nil {
		return nil, e
	}
	if owner != user {
		return nil, errors.New("linked project ownership mismatch")
	}
	return r, nil
}

func (s *Service) AdminOrderDetail(id string) (map[string]any, error) {
	o, e := scanAdminOrder(s.db.QueryRow(`SELECT `+adminOrderColumns+` FROM recharge_orders o JOIN users u ON u.id=o.user_id WHERE o.id=$1`, id))
	if e != nil {
		return nil, e
	}
	r, e := s.adminLinkedRun(o.RunID, o.UserID, "")
	if e != nil {
		return nil, e
	}
	refund, e := s.GetRefund(id)
	if e != nil {
		return nil, e
	}
	events, e := s.adminEvents(`(note LIKE $1 AND action IN ('refund_requested','refund_result')) OR (target_id=$2 AND action IN ('manual_order_approve','manual_order_reject'))`, []any{"order=" + id + " %", id})
	if e != nil {
		return nil, e
	}
	var runValue, refundValue any
	if r != nil {
		runValue = r
	}
	if refund != nil {
		cp := *refund
		cp.Error = adminOperationalError(cp.Error)
		refundValue = &cp
	}
	return map[string]any{"order": o, "user": map[string]any{"id": o.UserID, "email": o.User.Email}, "run": runValue, "refund": refundValue, "events": events}, nil
}

// AdminJob excludes payload, prompts and lease credentials from serialization.
type AdminJob struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"projectId"`
	ProjectTitle    string    `json:"projectTitle"`
	UserID          string    `json:"userId"`
	UserEmail       string    `json:"userEmail"`
	RunID           string    `json:"runId"`
	ShotID          string    `json:"shotId"`
	Kind            string    `json:"kind"`
	Status          string    `json:"status"`
	Phase           string    `json:"phase"`
	UpstreamID      string    `json:"upstreamId"`
	ErrorCode       string    `json:"errorCode"`
	LastError       string    `json:"lastError"`
	ReservedCredits int64     `json:"reservedCredits"`
	Attempts        int       `json:"attempts"`
	MaxAttempts     int       `json:"maxAttempts"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

const adminJobColumns = `j.id,j.project_id,p.title,p.user_id,u.email,COALESCE(j.run_id,''),COALESCE(j.shot_id,''),j.kind,j.status,j.phase,COALESCE(j.upstream_id,''),j.error_code,COALESCE(j.last_error,''),j.reserved_credits,j.attempts,j.max_attempts,j.updated_at`
const adminJobFrom = ` FROM jobs j JOIN projects p ON p.id=j.project_id JOIN users u ON u.id=p.user_id`

func scanAdminJob(row adminScanner) (*AdminJob, error) {
	j := new(AdminJob)
	e := row.Scan(&j.ID, &j.ProjectID, &j.ProjectTitle, &j.UserID, &j.UserEmail, &j.RunID, &j.ShotID, &j.Kind, &j.Status, &j.Phase, &j.UpstreamID, &j.ErrorCode, &j.LastError, &j.ReservedCredits, &j.Attempts, &j.MaxAttempts, &j.UpdatedAt)
	if e != nil {
		return nil, e
	}
	j.LastError = adminOperationalError(j.LastError)
	return j, nil
}

// Raw upstream errors can contain request bodies, URLs with tokens, and prompts.
// Keep the error code for triage while withholding the untrusted transport text.
func adminOperationalError(value string) string {
	if value == "" {
		return ""
	}
	return "详细错误已隐藏，请依据错误代码核对"
}
func (s *Service) AdminJobsList(kw, status string, page, size int) (*model.PageResp, error) {
	page, size = adminPaging(page, size)
	where := adminJobFrom + ` WHERE ($1='' OR j.id ILIKE $2 OR p.title ILIKE $2 OR u.email ILIKE $2 OR COALESCE(j.upstream_id,'') ILIKE $2 OR COALESCE(j.run_id,'') ILIKE $2) AND ($3='' OR j.status=$3)`
	args := []any{strings.TrimSpace(kw), "%" + strings.TrimSpace(kw) + "%", status}
	var total int
	if e := s.db.QueryRow(`SELECT count(*)`+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT `+adminJobColumns+where+` ORDER BY j.updated_at DESC,j.id DESC LIMIT $4 OFFSET $5`, append(args, size, int64(page-1)*int64(size))...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []*AdminJob{}
	for rows.Next() {
		j, e := scanAdminJob(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, j)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return &model.PageResp{Items: items, Total: total, Page: page, PageSize: size}, nil
}
func (s *Service) AdminJobDetail(id string) (map[string]any, error) {
	j, e := scanAdminJob(s.db.QueryRow(`SELECT `+adminJobColumns+adminJobFrom+` WHERE j.id=$1`, id))
	if e != nil {
		return nil, e
	}
	r, e := s.adminLinkedRun(j.RunID, j.UserID, j.ProjectID)
	if e != nil {
		return nil, e
	}
	events, e := s.adminEvents(`project_id=$1 AND (shot_id IS NULL OR shot_id=$2)`, []any{j.ProjectID, j.ShotID})
	if e != nil {
		return nil, e
	}
	var runValue any
	if r != nil {
		runValue = r
	}
	shots, e := s.adminJobShots(j)
	if e != nil {
		return nil, e
	}
	receipts, e := s.adminJobReceipts(j)
	if e != nil {
		return nil, e
	}
	return map[string]any{"job": j, "run": runValue, "events": events, "shots": shots, "receipts": receipts}, nil
}

type AdminCredit struct {
	model.CreditTransaction
	UserEmail string `json:"userEmail"`
}

const adminCreditColumns = `c.id,c.user_id,u.email,c.amount,c.balance_after,c.kind,COALESCE(c.project_id,''),COALESCE(c.note,''),COALESCE(c.operator_id,''),c.created_at`

func scanAdminCredit(row adminScanner) (*AdminCredit, error) {
	c := new(AdminCredit)
	e := row.Scan(&c.ID, &c.UserID, &c.UserEmail, &c.Amount, &c.BalanceAfter, &c.Kind, &c.ProjectID, &c.Note, &c.OperatorID, &c.CreatedAt)
	return c, e
}
func (s *Service) AdminCredits(kw string, page, size int) (*model.PageResp, error) {
	page, size = adminPaging(page, size)
	where := ` FROM credit_transactions c JOIN users u ON u.id=c.user_id WHERE ($1='' OR u.email ILIKE $2 OR c.user_id ILIKE $2 OR c.id ILIKE $2 OR c.kind ILIKE $2)`
	args := []any{strings.TrimSpace(kw), "%" + strings.TrimSpace(kw) + "%"}
	var total int
	if e := s.db.QueryRow(`SELECT count(*)`+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT `+adminCreditColumns+where+` ORDER BY c.created_at DESC,c.id DESC LIMIT $3 OFFSET $4`, append(args, size, int64(page-1)*int64(size))...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []*AdminCredit{}
	for rows.Next() {
		c, e := scanAdminCredit(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return &model.PageResp{Items: items, Total: total, Page: page, PageSize: size}, nil
}
func (s *Service) AdminUserDetail(id string) (map[string]any, error) {
	var u model.User
	var reserved, projects, orders int64
	e := s.db.QueryRow(`SELECT u.id,u.email,COALESCE(u.nickname,''),u.role,u.credits,u.status,u.created_at,u.updated_at,u.session_version,
 COALESCE((SELECT sum(j.reserved_credits) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id=u.id AND j.status IN ('queued','running','reconcile')),0),
 (SELECT count(*) FROM projects WHERE user_id=u.id),(SELECT count(*) FROM recharge_orders WHERE user_id=u.id) FROM users u WHERE u.id=$1`, id).Scan(
		&u.ID, &u.Email, &u.Nickname, &u.Role, &u.Credits, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.SessionVersion, &reserved, &projects, &orders)
	if e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT `+adminCreditColumns+` FROM credit_transactions c JOIN users u ON u.id=c.user_id WHERE c.user_id=$1 ORDER BY c.created_at DESC,c.id DESC LIMIT 100`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []*AdminCredit{}
	for rows.Next() {
		c, e := scanAdminCredit(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	available := u.Credits - reserved
	if available < 0 {
		available = 0
	}
	return map[string]any{"user": &u, "version": u.SessionVersion, "availableCredits": available, "reservedCredits": reserved, "projectsCount": projects, "ordersCount": orders, "transactions": items}, nil
}

const adminAuditColumns = `id,COALESCE(user_id,''),COALESCE(project_id,''),COALESCE(shot_id,''),action,COALESCE(from_status,''),COALESCE(to_status,''),COALESCE(note,''),created_at`

func scanAdminAudit(row adminScanner) (*model.AuditLog, error) {
	a := new(model.AuditLog)
	e := row.Scan(&a.ID, &a.UserID, &a.ProjectID, &a.ShotID, &a.Action, &a.FromStatus, &a.ToStatus, &a.Note, &a.CreatedAt)
	if e != nil {
		return nil, e
	}
	// Audit notes historically also store arbitrary generation/provider text.
	// Display structure and state changes; never forward those raw notes.
	if a.Note != "" {
		raw := a.Note
		a.Note = "详细记录已隐藏"
		switch a.Action {
		case "credits_adjust", "user_status", "manual_order_approve", "manual_order_reject":
			var info struct {
				Target string `json:"target"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal([]byte(raw), &info) == nil && info.Target != "" && len(info.Target) <= 64 && len(info.Reason) <= 1024 {
				a.Note = "目标：" + info.Target + "；原因：" + info.Reason
			}
		}
	}
	return a, nil
}
func (s *Service) adminEvents(where string, args []any) ([]*model.AuditLog, error) {
	rows, e := s.db.Query(`SELECT `+adminAuditColumns+` FROM audit_logs WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT 100`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []*model.AuditLog{}
	for rows.Next() {
		a, e := scanAdminAudit(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const adminAuditSource = `(SELECT id,user_id,project_id,shot_id,action,from_status,to_status,note,created_at FROM audit_logs
 UNION ALL SELECT 'partner:'||id,actor_id,NULL::varchar,NULL::varchar,'partner:'||action,NULL::varchar,NULL::varchar,NULL::text,created_at FROM partner_audit_events) a`

func (s *Service) AdminAudit(kw string, page, size int) (*model.PageResp, error) {
	page, size = adminPaging(page, size)
	where := ` FROM ` + adminAuditSource + ` WHERE ($1='' OR id ILIKE $2 OR COALESCE(user_id,'') ILIKE $2 OR COALESCE(project_id,'') ILIKE $2 OR action ILIKE $2)`
	args := []any{strings.TrimSpace(kw), "%" + strings.TrimSpace(kw) + "%"}
	var total int
	if e := s.db.QueryRow(`SELECT count(*)`+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := s.db.Query(`SELECT `+adminAuditColumns+where+` ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, append(args, size, int64(page-1)*int64(size))...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []*model.AuditLog{}
	for rows.Next() {
		a, e := scanAdminAudit(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return &model.PageResp{Items: out, Total: total, Page: page, PageSize: size}, nil
}

// Timeline projections deliberately omit visual prompts, raw provider receipts and URLs.
type AdminShot struct {
	ID         string    `json:"id"`
	Seq        int       `json:"seq"`
	Status     string    `json:"status"`
	UpstreamID string    `json:"upstreamId"`
	UpdatedAt  time.Time `json:"updatedAt"`
}
type AdminReceipt struct {
	ID           string     `json:"id"`
	UpstreamID   string     `json:"upstreamId"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	ChargeMicros int64      `json:"chargeMicros"`
	Credits      int64      `json:"credits"`
	CreatedAt    time.Time  `json:"createdAt"`
	SettledAt    *time.Time `json:"settledAt"`
}

func (s *Service) adminJobShots(j *AdminJob) ([]AdminShot, error) {
	rows, e := s.db.Query(`SELECT id,seq,status,COALESCE(upstream_id,''),updated_at FROM shots WHERE project_id=$1 AND ($2='assemble' OR id=$3) ORDER BY seq,id`, j.ProjectID, j.Kind, j.ShotID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []AdminShot{}
	for rows.Next() {
		var v AdminShot
		if e = rows.Scan(&v.ID, &v.Seq, &v.Status, &v.UpstreamID, &v.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) adminJobReceipts(j *AdminJob) ([]AdminReceipt, error) {
	rows, e := s.db.Query(`SELECT id,upstream_id,kind,status,charge_micros,credits,created_at,settled_at FROM generation_settlements
 WHERE project_id=$1 AND user_id=$2 AND ($3='assemble' OR (upstream_id=$4 AND COALESCE(shot_id,'')=$5 AND kind=CASE WHEN $3='shot' THEN 'video' ELSE 'image' END)) ORDER BY created_at DESC,id DESC`, j.ProjectID, j.UserID, j.Kind, j.UpstreamID, j.ShotID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []AdminReceipt{}
	for rows.Next() {
		var v AdminReceipt
		if e = rows.Scan(&v.ID, &v.UpstreamID, &v.Kind, &v.Status, &v.ChargeMicros, &v.Credits, &v.CreatedAt, &v.SettledAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
