package service

import (
	"database/sql"
	"errors"
)

var (
	ErrProjectNotFound = errors.New("项目不存在")
	ErrProjectBusy     = errors.New("项目仍在生成或有待结算任务，暂不能删除")
)

// DeleteProject soft-deletes a historical project. Financial receipts, credit
// transactions and audit rows are intentionally retained for reconciliation.
func (s *Service) DeleteProject(userID, projectID string, force bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner, _, _, _, err := lockQueueProject(tx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProjectNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrProjectNotFound
	}
	var activeJobs, reconcileJobs, activeRuns, pendingSettlements int
	if err = tx.QueryRow(`SELECT
		(SELECT count(*) FROM jobs WHERE project_id=$1 AND status IN ('queued','running')),
		(SELECT count(*) FROM jobs WHERE project_id=$1 AND status='reconcile'),
		(SELECT count(*) FROM generation_runs WHERE project_id=$1 AND status IN ('queued','running','waiting_credits','settlement_pending')),
		(SELECT count(*) FROM generation_settlements WHERE project_id=$1 AND status='pending')`, projectID).
		Scan(&activeJobs, &reconcileJobs, &activeRuns, &pendingSettlements); err != nil {
		return err
	}
	if activeJobs > 0 || activeRuns > 0 || pendingSettlements > 0 || (!force && reconcileJobs > 0) {
		return ErrProjectBusy
	}
	var status string
	if err = tx.QueryRow(`SELECT status FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&status); err != nil {
		return ErrProjectNotFound
	}
	res, err := tx.Exec(`UPDATE projects SET deleted_at=now(),deleted_by=$2,updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, projectID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return err
		}
		return ErrProjectNotFound
	}
	note := "soft delete; financial records retained"
	if force && reconcileJobs > 0 {
		note = "forced soft delete; unresolved upstream jobs retained; financial records retained"
	}
	if _, err = tx.Exec(`INSERT INTO audit_logs(user_id,project_id,action,from_status,to_status,note) VALUES($1,$2,'delete_project',$3,'DELETED',$4)`, userID, projectID, status, note); err != nil {
		return err
	}
	return tx.Commit()
}
