package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// StopAutomaticResult reports the one-time transition from automatic runs to
// manual projects. Runs that are already terminal are not counted as failures.
type StopAutomaticResult struct {
	Cancelled int
	Failed    []string
}

// StopAllAutomaticRuns stops new stage dispatch for every active run while
// preserving already submitted upstream work for polling and settlement.
// It deliberately calls CancelRun instead of writing generation_runs/jobs
// directly: that path releases only unsubmitted reservations and never deletes
// provider identities or settled receipts.
func (s *Service) StopAllAutomaticRuns(ctx context.Context) (*StopAutomaticResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,project_id FROM generation_runs
		WHERE status IN ('queued','running','waiting_credits','settlement_pending')
		ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	type active struct{ id, user, project string }
	var runs []active
	for rows.Next() {
		var r active
		if err = rows.Scan(&r.id, &r.user, &r.project); err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := &StopAutomaticResult{}
	for _, r := range runs {
		if err = s.CancelRun(r.user, r.project, r.id); err != nil {
			// A concurrent worker/user may have reached a terminal state after
			// the listing. Treat that as already stopped, not as a failure.
			var status string
			qerr := s.db.QueryRowContext(ctx, `SELECT status FROM generation_runs WHERE id=$1`, r.id).Scan(&status)
			if qerr == nil && (status == "cancelled" || status == "completed" || status == "failed") {
				continue
			}
			if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
				return nil, qerr
			}
			result.Failed = append(result.Failed, r.id+":"+err.Error())
			continue
		}
		result.Cancelled++
	}
	// All projects are manual after this operation, including projects whose
	// runs were already terminal. The column defaults to manual for new rows.
	if _, err = s.db.ExecContext(ctx, `UPDATE projects SET workflow_mode='manual',updated_at=now()
		WHERE workflow_mode IS DISTINCT FROM 'manual'`); err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		s.Log("", "", "", "stop_automatic_mode", "", "manual",
			fmt.Sprintf("cancelled=%d failed=%d", result.Cancelled, len(result.Failed)))
	}
	return result, nil
}
