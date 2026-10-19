package run

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// 充值到账后唤醒续跑的持久化 outbox。
// 关键不变量：写入必须与入账同事务；消费可重复投递；崩溃后可补扫。

const wakeupMaxAttempts = 5

type Wakeup struct {
	ID         string
	RunID      string
	OrderID    string
	UserID     string
	Attempts   int
	LeaseToken int64
}

// EnqueueInTx 在「充值入账」的同一事务里写入唤醒记录。
// 同一 (run, order) 重复入账只会产生一条，重复回调不会重复续跑。
func (s *Store) EnqueueInTx(ctx context.Context, tx *sql.Tx, runID, orderID, userID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO resume_wakeups(id,run_id,order_id,user_id)
	 VALUES($1,$2,$3,$4) ON CONFLICT (run_id, order_id) DO NOTHING`,
		newID(), runID, orderID, userID)
	return err
}

// ClaimDue 由 worker 调用：抢占待处理唤醒，避免多 worker 重复处理。
func (s *Store) ClaimDue(ctx context.Context, limit int) ([]Wakeup, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH picked AS (
		  SELECT id FROM resume_wakeups
		  WHERE status='pending' AND run_after <= now()
		  ORDER BY run_after
		  LIMIT $1
		  FOR UPDATE SKIP LOCKED
		)
		UPDATE resume_wakeups w SET status='claimed', claimed_at=now(), lease_token=lease_token+1
		FROM picked WHERE w.id = picked.id
		RETURNING w.id, w.run_id, w.order_id, w.user_id, w.attempts, w.lease_token`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Wakeup{}
	for rows.Next() {
		var w Wakeup
		if err = rows.Scan(&w.ID, &w.RunID, &w.OrderID, &w.UserID, &w.Attempts, &w.LeaseToken); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkDone 只允许当前租约持有者提交结果；旧消费者不能覆盖新消费者。
func (s *Store) MarkDone(ctx context.Context, id string, leaseToken int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resume_wakeups SET status='done', last_error=''
		WHERE id=$1 AND lease_token=$2 AND status='claimed'`, id, leaseToken)
	return err
}

// MarkFailed 失败退避重试，超过上限进入 dead（保留记录，便于人工补扫）。
func (s *Store) MarkFailed(ctx context.Context, id string, leaseToken int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE resume_wakeups
	 SET attempts=attempts+1,
	     status=CASE WHEN attempts+1 >= $2 THEN 'dead' ELSE 'pending' END,
	     run_after=now()+ (interval '10 seconds') * (attempts+1),
	     last_error=$3
	 WHERE id=$1 AND lease_token=$4 AND status='claimed'`, id, wakeupMaxAttempts, reason, leaseToken)
	return err
}

// ReclaimStale 回收租约过期的 claimed 记录：worker 崩溃后不会永久卡死。
// 只重置租约，不消耗 attempts；真正的业务失败由 MarkFailed 计数。
func (s *Store) ReclaimStale(ctx context.Context, age time.Duration) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE resume_wakeups
	 SET status='pending', claimed_at=NULL, lease_token=lease_token+1, run_after=now()
	 WHERE status='claimed' AND claimed_at < now()-$1::interval`,
		fmt.Sprintf("%d seconds", int(age.Seconds())))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// BackfillMissing 补扫「已入账且带 run 绑定、但没有唤醒记录」的订单。
// 入账事务已经保证同写；这里只修复历史数据或极端故障留下的缺口。
func (s *Store) BackfillMissing(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO resume_wakeups(id,run_id,order_id,user_id)
		SELECT substr(md5(o.id || r.id),1,32), o.run_id, o.id, o.user_id
		FROM recharge_orders o
		JOIN generation_runs r ON r.id=o.run_id AND r.user_id=o.user_id
		WHERE o.status='paid' AND o.credited AND COALESCE(o.run_id,'')<>''
		ON CONFLICT (run_id, order_id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResumeRun 唤醒被消费后的实际动作：只把 run 从等待态放回 queued。
// actorUserID 必须是该 run 的所有者：别人的充值绝不能启动你的执行。
// 其余校验（账号状态、版本、余额、互斥）仍由编排层执行。
func (s *Store) ResumeRun(ctx context.Context, runID, actorUserID string) (bool, error) {
	reason, err := s.ResumeRunChecked(ctx, runID, actorUserID)
	return reason == "resumed", err
}

// ResumeRunChecked 与 ResumeRun 相同，但返回未续跑的原因，便于 worker 记录与测试。
func (s *Store) ResumeRunChecked(ctx context.Context, runID, actorUserID string) (string, error) {
	if actorUserID == "" {
		return "missing_actor", nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// 六.5：余额仍不足则继续等待，不反复提交、不循环扣费。
	var est, required int64
	var status, projectID, owner, projectOwner, userStatus, waitingReason string
	var myVersion int
	var autoResume bool
	e := tx.QueryRowContext(ctx, `SELECT r.credits_estimated,r.required_credits,r.status,r.waiting_reason,r.project_version,r.project_id,
	       r.user_id,p.user_id,u.status,r.auto_resume
	 FROM generation_runs r
	 JOIN projects p ON p.id=r.project_id
	 JOIN users u ON u.id=r.user_id
	 WHERE r.id=$1 FOR UPDATE OF r`, runID).
		Scan(&est, &required, &status, &waitingReason, &myVersion, &projectID, &owner, &projectOwner, &userStatus, &autoResume)
	if errors.Is(e, sql.ErrNoRows) {
		return "missing", nil
	}
	if e != nil {
		return "", e
	}
	if owner != actorUserID {
		return "owner_mismatch", nil
	}
	if status != "waiting_credits" {
		return "not_waiting", nil
	}
	if !autoResume {
		return "auto_resume_off", nil
	}
	if projectOwner != actorUserID {
		return "project_changed", nil
	}
	if userStatus != "active" {
		return "account_inactive", nil
	}
	// 已被新版本替代的旧执行不得被充值唤醒。
	var newest int
	if e2 := tx.QueryRowContext(ctx, `SELECT COALESCE(max(project_version),0) FROM generation_runs
	 WHERE project_id=$1`, projectID).Scan(&newest); e2 != nil {
		return "", e2
	}
	if newest > myVersion {
		return "superseded", nil
	}
	var avail int64
	var e2 error
	if waitingReason == "settlement" {
		f, e := settlementFunding(ctx, tx, actorUserID, runID)
		if e != nil {
			return "", e
		}
		avail, required = f.Available, f.Required
	} else {
		// 新行用 required_credits（下一步总需求）；旧行没有该字段值时退回原估算口径。
		if required <= 0 {
			required = est
		}
		if required <= 0 {
			return "no_estimate", nil
		}
		avail, e2 = availableCredits(ctx, tx, actorUserID)
		if e2 != nil {
			return "", e2
		}
	}
	if avail < required {
		// 保持等待态，并如实记录统一的总需求口径；缺口由展示层实时计算。
		if _, e3 := tx.ExecContext(ctx, `UPDATE generation_runs
		 SET required_credits=$2, waiting_reason=CASE WHEN waiting_reason='' THEN 'balance' ELSE waiting_reason END, updated_at=now()
		 WHERE id=$1`, runID, required); e3 != nil {
			return "", e3
		}
		if e3 := tx.Commit(); e3 != nil {
			return "", e3
		}
		return "insufficient", nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE generation_runs
	 SET status='queued', waiting_reason='', required_credits=0, updated_at=now()
	 WHERE id=$1 AND user_id=$2 AND status='waiting_credits' AND auto_resume`, runID, actorUserID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "race_lost", nil
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return "resumed", nil
}
