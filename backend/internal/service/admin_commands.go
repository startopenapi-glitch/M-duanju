package service

import (
	"crypto/sha256"
	"database/sql"
	"drama-platform/internal/model"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"strings"
	"unicode/utf8"
)

var ErrAdminConflict = errors.New("记录已更新或请求内容冲突，请刷新后重试")
var ErrAdminInvalid = errors.New("参数无效，请检查金额、原因与用户")
var ErrAdminForbidden = errors.New("无权执行此操作")

func adminAuditTx(tx *sql.Tx, actor, action, target, from, to, reason string) error {
	payload, _ := json.Marshal(map[string]string{"target": target, "reason": reason})
	_, e := tx.Exec(`INSERT INTO audit_logs(user_id,action,from_status,to_status,note,target_id) VALUES($1,$2,$3,$4,$5,$6)`, actor, action, from, to, string(payload), target)
	return e
}
func activeAdminTx(tx *sql.Tx, id string) error {
	var yes bool
	if e := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='admin' AND status='active')`, id).Scan(&yes); e != nil {
		return e
	}
	if !yes {
		return ErrAdminForbidden
	}
	return nil
}

// AdjustCredits uses the same user lock as reservations and settlement. A retry
// reuses its request ID, and the audit, balance and ledger either all commit or none.
func (s *Service) AdjustCredits(actor string, req *model.AdjustCreditsReq) error {
	if req == nil || req.UserID == "" || len(req.UserID) > 64 || req.Amount == 0 || req.Amount == math.MinInt64 || strings.TrimSpace(req.Note) == "" || utf8.RuneCountInString(req.Note) > 255 {
		return ErrAdminInvalid
	}
	requestID, err := uuid.Parse(req.RequestID)
	if err != nil {
		return ErrAdminInvalid
	}
	payload, _ := json.Marshal(struct {
		User   string
		Amount int64
		Note   string
	}{req.UserID, req.Amount, req.Note})
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = activeAdminTx(tx, actor); err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO admin_action_requests(actor_id,request_id,payload_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, actor, requestID.String(), digest)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var old string
		if err = tx.QueryRow(`SELECT payload_hash FROM admin_action_requests WHERE actor_id=$1 AND request_id=$2`, actor, requestID.String()).Scan(&old); err != nil {
			return err
		}
		if old != digest {
			return ErrAdminConflict
		}
		return tx.Commit()
	}
	var bal, reserved int64
	if err = tx.QueryRow(`SELECT credits FROM users WHERE id=$1 FOR UPDATE`, req.UserID).Scan(&bal); err != nil {
		return err
	}
	if err = tx.QueryRow(`SELECT COALESCE(sum(reserved_credits),0) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id=$1`, req.UserID).Scan(&reserved); err != nil {
		return err
	}
	if req.Amount < 0 && (-req.Amount > bal || bal+req.Amount < reserved) {
		return fmt.Errorf("%w：可用积分不足，不能扣减在途预占积分", ErrAdminInvalid)
	}
	if req.Amount > 0 && bal > math.MaxInt64-req.Amount {
		return ErrAdminInvalid
	}
	next := bal + req.Amount
	if _, err = tx.Exec(`UPDATE users SET credits=$1,updated_at=now() WHERE id=$2`, next, req.UserID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO credit_transactions(user_id,amount,balance_after,kind,note,operator_id) VALUES($1,$2,$3,'admin_adjust',$4,$5)`, req.UserID, req.Amount, next, req.Note, actor); err != nil {
		return err
	}
	if err = adminAuditTx(tx, actor, "credits_adjust", req.UserID, "", "", req.Note); err != nil {
		return err
	}
	return tx.Commit()
}

type AdminUserStatusResult struct {
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

func (s *Service) SetUserStatus(actor, target, status, reason string, version int64) (*AdminUserStatusResult, error) {
	if (status != "active" && status != "banned") || version < 1 || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 255 {
		return nil, ErrAdminInvalid
	}
	if actor == target && status == "banned" {
		return nil, ErrAdminForbidden
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('admin-user-status'))`); err != nil {
		return nil, err
	}
	if err = activeAdminTx(tx, actor); err != nil {
		return nil, err
	}
	var current, role string
	var actual int64
	if err = tx.QueryRow(`SELECT status,role,session_version FROM users WHERE id=$1 FOR UPDATE`, target).Scan(&current, &role, &actual); err != nil {
		return nil, err
	}
	if actual != version {
		return nil, ErrAdminConflict
	}
	if current == status {
		return &AdminUserStatusResult{current, actual}, tx.Commit()
	}
	if role == "admin" && status == "banned" {
		var n int
		if err = tx.QueryRow(`SELECT count(*) FROM users WHERE role='admin' AND status='active'`).Scan(&n); err != nil {
			return nil, err
		}
		if n <= 1 {
			return nil, ErrAdminForbidden
		}
	}
	// Existing DB trigger revokes sessions on both disable and restore.
	if err = tx.QueryRow(`UPDATE users SET status=$1,updated_at=now() WHERE id=$2 RETURNING session_version`, status, target).Scan(&actual); err != nil {
		return nil, err
	}
	if err = adminAuditTx(tx, actor, "user_status", target, current, status, reason); err != nil {
		return nil, err
	}
	return &AdminUserStatusResult{status, actual}, tx.Commit()
}
