package service

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// GenerationSettlement contains verified facts from one completed upstream task.
// It is deliberately independent of delivery/polling and never creates an upstream task.
type GenerationSettlement struct {
	Kind, TaskID, ProjectID, ShotID, URL string
	ChargeMicros                         int64
	Duration                             float64
	Width, Height                        int
}

type SettlementPendingError struct {
	Reason              string
	Required, Available int64
}

func (e *SettlementPendingError) Error() string {
	return fmt.Sprintf("生成已完成，结算待处理（%s）：需要%d积分，可用%d积分", e.Reason, e.Required, e.Available)
}

// SettleGeneration atomically records a receipt, debit, ledger and deliverable state.
// true means this invocation settled it; replay returns false without another debit.
// Lock order is user -> project -> shot -> receipt, also used by queue reservations.
func (s *Service) SettleGeneration(in GenerationSettlement) (bool, error) {
	if (in.Kind != "video" && in.Kind != "image") || in.TaskID == "" || in.ProjectID == "" || in.URL == "" || in.ChargeMicros < 0 || math.IsNaN(in.Duration) || math.IsInf(in.Duration, 0) || in.Duration < 0 || in.Width < 0 || in.Height < 0 {
		return false, errors.New("invalid settlement facts")
	}
	if (in.Kind == "video" && in.ShotID == "") || (in.Kind == "image" && in.ShotID != "") {
		return false, errors.New("invalid settlement target")
	}
	credits := videoChargeCredits(in.ChargeMicros)
	if in.Kind == "image" {
		credits = 30
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var uid string
	if err = tx.QueryRow(`SELECT user_id FROM projects WHERE id=$1`, in.ProjectID).Scan(&uid); err != nil {
		return false, err
	}
	// A known run binds the financial owner before provider submission. Never
	// reinterpret that payer as the current project owner after a transfer.
	var runOwner string
	ownerErr := tx.QueryRow(`SELECT r.user_id FROM jobs j JOIN generation_runs r ON r.id=j.run_id
      WHERE j.project_id=$1 AND j.kind=$2 AND j.upstream_id=$3 AND COALESCE(j.shot_id,'')=$4`, in.ProjectID, map[string]string{"video": JobShot, "image": JobKeyVisual}[in.Kind], in.TaskID, in.ShotID).Scan(&runOwner)
	if ownerErr == nil {
		uid = runOwner
	} else if !errors.Is(ownerErr, sql.ErrNoRows) {
		return false, ownerErr
	}
	var balance, spent, budget int64
	if err = tx.QueryRow(`SELECT credits FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&balance); err != nil {
		return false, err
	}
	var owner, currentTask, projectStatus string
	if err = tx.QueryRow(`SELECT user_id,spent_credits,budget_credits,COALESCE(keyvisual_task_id,''),status FROM projects WHERE id=$1 FOR UPDATE`, in.ProjectID).Scan(&owner, &spent, &budget, &currentTask, &projectStatus); err != nil {
		return false, err
	}
	if owner != uid {
		return false, &SettlementPendingError{Reason: "ownership", Required: credits}
	}
	var shotStatus, videoURL, deliveredTask string
	if in.Kind == "video" {
		if err = tx.QueryRow(`SELECT COALESCE(upstream_id,''),status,COALESCE(video_url,''),COALESCE(delivered_upstream_id,'') FROM shots WHERE id=$1 AND project_id=$2 FOR UPDATE`, in.ShotID, in.ProjectID).Scan(&currentTask, &shotStatus, &videoURL, &deliveredTask); err != nil {
			return false, err
		}
	}
	if s.job != nil {
		if err = s.fenceTx(tx, in.ProjectID, in.ShotID, in.TaskID); err != nil {
			return false, err
		}
	}
	// The upgrade migration protects known historical artifacts. Also protect legacy
	// completed records encountered during a rolling upgrade before changing attempts.
	if in.Kind == "video" {
		if err = protectLegacyShot(tx, uid, in.ProjectID, in.ShotID, currentTask, shotStatus, videoURL, deliveredTask); err != nil {
			return false, err
		}
	}
	var receiptID, receiptStatus, receiptProject, receiptShot, receiptUser, receiptURL string
	var receiptMicros, receiptCredits int64
	var receiptDuration float64
	var receiptWidth, receiptHeight int
	lookup := func() error {
		return tx.QueryRow(`SELECT id,status,project_id,COALESCE(shot_id,''),user_id,charge_micros,credits,result_url,result_duration,result_width,result_height FROM generation_settlements WHERE provider='limeng' AND kind=$1 AND upstream_id=$2 FOR UPDATE`, in.Kind, in.TaskID).Scan(&receiptID, &receiptStatus, &receiptProject, &receiptShot, &receiptUser, &receiptMicros, &receiptCredits, &receiptURL, &receiptDuration, &receiptWidth, &receiptHeight)
	}
	err = lookup()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		if currentTask != in.TaskID || (in.Kind == "video" && shotStatus != "QUEUED") || (in.Kind == "image" && projectStatus != "KEYVISUAL_GENERATING") {
			return false, errors.New("stale generation task")
		}
		// DO NOTHING may affect zero rows only for a concurrent unique-key winner;
		// re-read its locked receipt and compare ownership and every immutable fact.
		res, e := tx.Exec(`INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url,result_duration,result_width,result_height) VALUES('limeng',$1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10,$11) ON CONFLICT(provider,kind,upstream_id) DO NOTHING`, in.Kind, in.TaskID, in.ProjectID, nullIfEmpty(in.ShotID), uid, in.ChargeMicros, credits, in.URL, in.Duration, in.Width, in.Height)
		if e != nil {
			return false, e
		}
		if n, e := res.RowsAffected(); e != nil || (n != 0 && n != 1) {
			return false, fmt.Errorf("receipt insert affected %d rows: %v", n, e)
		}
		if err = lookup(); err != nil {
			return false, err
		}
	}
	if receiptProject != in.ProjectID || receiptShot != in.ShotID || receiptUser != uid {
		return false, errors.New("conflicting settlement facts")
	}
	// Legacy amounts are intentionally unknown. Never infer a debit from a replay.
	if receiptStatus == "legacy_unverified" {
		return false, commitSettlement(tx)
	}
	if receiptMicros != in.ChargeMicros || receiptCredits != credits || receiptURL != in.URL || receiptDuration != in.Duration || receiptWidth != in.Width || receiptHeight != in.Height {
		return false, errors.New("conflicting settlement facts")
	}
	if receiptStatus == "settled" {
		return false, commitSettlement(tx)
	}
	if currentTask != in.TaskID || (in.Kind == "video" && shotStatus != "QUEUED") || (in.Kind == "image" && projectStatus != "KEYVISUAL_GENERATING") {
		return false, errors.New("stale generation task")
	}
	if err = s.fenceTx(tx, in.ProjectID, in.ShotID, in.TaskID); err != nil {
		return false, err
	}
	var otherUserReserved, otherProjectReserved int64
	ownJob := ""
	if s.job != nil {
		ownJob = s.job.ID
	}
	if err = tx.QueryRow(`SELECT COALESCE(sum(j.reserved_credits),0),COALESCE(sum(j.reserved_credits) FILTER(WHERE j.project_id=$2),0) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id=$1 AND j.id<>$3`, uid, in.ProjectID, ownJob).Scan(&otherUserReserved, &otherProjectReserved); err != nil {
		return false, err
	}
	var pending *SettlementPendingError
	if balance-otherUserReserved < credits {
		pending = &SettlementPendingError{"balance", credits, balance - otherUserReserved}
	} else if spent > budget || credits > budget-spent-otherProjectReserved {
		pending = &SettlementPendingError{"budget", credits, budget - spent - otherProjectReserved}
	}
	if pending != nil {
		if err = settlementExecOne(tx, "pending reason", `UPDATE generation_settlements SET pending_reason=$1,pending_error=NULL WHERE id=$2 AND status='pending'`, pending.Reason, receiptID); err != nil {
			return false, err
		}
		if err = commitSettlement(tx); err != nil {
			return false, err
		}
		return false, pending
	}
	// Keep the upstream success facts outside the financial savepoint. Any ordinary
	// write error rolls back all money/result changes, then commits only diagnostics.
	if _, err = tx.Exec(`SAVEPOINT settlement_financial`); err != nil {
		return false, err
	}
	writePending := func(cause error) (bool, error) {
		if _, e := tx.Exec(`ROLLBACK TO SAVEPOINT settlement_financial`); e != nil {
			return false, fmt.Errorf("settlement rollback failed; pending not confirmed: %w", errors.Join(cause, e))
		}
		if e := settlementExecOne(tx, "pending diagnostic", `UPDATE generation_settlements SET pending_reason='write',pending_error=$1 WHERE id=$2 AND status='pending'`, cause.Error(), receiptID); e != nil {
			return false, fmt.Errorf("pending diagnostic not persisted: %w", errors.Join(cause, e))
		}
		if e := commitSettlement(tx); e != nil {
			return false, fmt.Errorf("pending not confirmed: %w", errors.Join(cause, e))
		}
		return false, fmt.Errorf("generation settlement pending (write): %w", cause)
	}
	var ledgerID string
	if credits > 0 {
		if err = settlementExecOne(tx, "debit", `UPDATE users SET credits=credits-$1,updated_at=now() WHERE id=$2 AND credits >= $1`, credits, uid); err != nil {
			return writePending(err)
		}
		if err = tx.QueryRow(`INSERT INTO credit_transactions(user_id,amount,balance_after,kind,project_id,note) VALUES($1,$2,$3,'spend',$4,$5) RETURNING id`, uid, -credits, balance-credits, in.ProjectID, "generation:"+in.Kind+":"+in.TaskID).Scan(&ledgerID); err != nil {
			return writePending(err)
		}
		if err = settlementExecOne(tx, "project total", `UPDATE projects SET spent_credits=spent_credits+$1,updated_at=now() WHERE id=$2`, credits, in.ProjectID); err != nil {
			return writePending(err)
		}
	}
	publishCurrent, e := s.recordRunArtifactTx(tx, in.Kind, receiptURL, in.TaskID, in)
	if e != nil {
		return writePending(e)
	}
	// Apply stored facts only, conditioned on the still-current attempt/status.
	if !publishCurrent {
		err = nil // The immutable artifact belongs to the old run; do not publish over its successor.
	} else if in.Kind == "video" {
		err = settlementExecOne(tx, "video delivery", `UPDATE shots SET status='GENERATED',video_url=$1,duration_actual=$2,width=$3,height=$4,charge_micros=$5,delivered_upstream_id=$8,error_msg=NULL,updated_at=now() WHERE id=$6 AND project_id=$7 AND upstream_id=$8 AND status='QUEUED'`, receiptURL, receiptDuration, receiptWidth, receiptHeight, receiptMicros, in.ShotID, in.ProjectID, in.TaskID)
	} else {
		err = settlementExecOne(tx, "image delivery", `UPDATE projects SET status='KEYVISUAL_REVIEW',keyvisual_url=$1,keyvisual_task_id=NULL,error_msg=NULL,updated_at=now() WHERE id=$2 AND keyvisual_task_id=$3 AND status='KEYVISUAL_GENERATING'`, receiptURL, in.ProjectID, in.TaskID)
	}
	if err != nil {
		return writePending(err)
	}
	if err = settlementExecOne(tx, "receipt settlement", `UPDATE generation_settlements SET status='settled',credit_transaction_id=$1,pending_reason=NULL,pending_error=NULL,settled_at=now() WHERE id=$2 AND status='pending'`, nullIfEmpty(ledgerID), receiptID); err != nil {
		return writePending(err)
	}
	if s.job != nil {
		if err = settlementExecOne(tx, "release reservation", `UPDATE jobs SET reserved_credits=0,phase='delivered',error_code='',last_error=NULL WHERE id=$1 AND lease_token=$2 AND status='running'`, s.job.ID, s.job.LeaseToken); err != nil {
			return writePending(err)
		}
	}
	if publishCurrent && in.Kind == "video" {
		if _, err = tx.Exec(`UPDATE projects SET status='SHOTS_REVIEW',updated_at=now() WHERE id=$1 AND status='SHOTS_GENERATING' AND NOT EXISTS(SELECT 1 FROM shots WHERE project_id=$1 AND status IN ('PENDING','QUEUED'))`, in.ProjectID); err != nil {
			return writePending(err)
		}
	}
	if err = commitSettlement(tx); err != nil {
		return false, err
	}
	return true, nil
}

func commitSettlement(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit generation settlement (persistence unconfirmed; re-query original task): %w", err)
	}
	return nil
}

func settlementExecOne(tx *sql.Tx, operation, query string, args ...interface{}) error {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("%s affected %d rows: %v", operation, n, err)
	}
	return nil
}

// Called only after user -> project -> shot locks. Task3 must preserve this order
// and persist the old delivered identity before installing any new upstream id.
func protectLegacyShot(tx *sql.Tx, uid, projectID, shotID, taskID, status, artifact, deliveredTask string) error {
	// A trusted delivered identity wins over the mutable current attempt/status.
	// Without one, only confirmed completed video states bind the current id.
	protectedTask := deliveredTask
	if protectedTask == "" {
		if taskID == "" || (status != "GENERATED" && status != "APPROVED") {
			return nil
		}
		protectedTask = taskID
	}
	res, err := tx.Exec(`INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url) VALUES('limeng','video',$1,$2,$3,$4,0,0,'legacy_unverified',$5) ON CONFLICT(provider,kind,upstream_id) DO NOTHING`, protectedTask, projectID, shotID, uid, artifact)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || (n != 0 && n != 1) {
		return fmt.Errorf("legacy protection affected %d rows: %v", n, err)
	}
	if deliveredTask == "" {
		return settlementExecOne(tx, "legacy artifact identity", `UPDATE shots SET delivered_upstream_id=$1 WHERE id=$2 AND project_id=$3`, protectedTask, shotID, projectID)
	}
	return nil
}

// checkGenerationDelivery rejects known metadata conflicts/stale attempts before
// network or conversion work. It is advisory: SettleGeneration repeats all checks
// under locks after staging, including the content-addressed result URL.
func (s *Service) checkGenerationDelivery(in GenerationSettlement) (legacy bool, err error) {
	var owner, current, status string
	if in.Kind == "video" {
		err = s.db.QueryRow(`SELECT p.user_id,COALESCE(s.upstream_id,''),s.status FROM shots s JOIN projects p ON p.id=s.project_id WHERE s.id=$1 AND s.project_id=$2`, in.ShotID, in.ProjectID).Scan(&owner, &current, &status)
	} else {
		err = s.db.QueryRow(`SELECT user_id,COALESCE(keyvisual_task_id,''),status FROM projects WHERE id=$1`, in.ProjectID).Scan(&owner, &current, &status)
	}
	if err != nil {
		return false, err
	}
	var receiptStatus, project, shot, user string
	var micros, credits int64
	var duration float64
	var width, height int
	err = s.db.QueryRow(`SELECT status,project_id,COALESCE(shot_id,''),user_id,charge_micros,credits,result_duration,result_width,result_height FROM generation_settlements WHERE provider='limeng' AND kind=$1 AND upstream_id=$2`, in.Kind, in.TaskID).Scan(&receiptStatus, &project, &shot, &user, &micros, &credits, &duration, &width, &height)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil {
		if project != in.ProjectID || shot != in.ShotID || user != owner {
			return false, errors.New("conflicting settlement facts")
		}
		if receiptStatus == "legacy_unverified" {
			return true, nil
		}
		expected := videoChargeCredits(in.ChargeMicros)
		if in.Kind == "image" {
			expected = 30
		}
		if micros != in.ChargeMicros || credits != expected || duration != in.Duration || width != in.Width || height != in.Height {
			return false, errors.New("conflicting settlement facts")
		}
		if receiptStatus == "settled" {
			return false, nil
		}
	}
	if err = s.checkLease(); err != nil {
		return false, err
	}
	if s.job == nil {
		var active bool
		if err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND COALESCE(shot_id,'')=$2 AND kind=$3 AND status IN ('queued','running','reconcile'))`, in.ProjectID, in.ShotID, map[bool]string{true: JobShot, false: JobKeyVisual}[in.Kind == "video"]).Scan(&active); err != nil {
			return false, err
		}
		if active {
			return false, ErrLeaseLost
		}
	}
	if current != in.TaskID || (in.Kind == "video" && status != "QUEUED" && status != "GENERATED" && status != "APPROVED") || (in.Kind == "image" && status != "KEYVISUAL_GENERATING") {
		return false, errors.New("stale generation task")
	}
	return false, nil
}

// videoChargeCredits preserves production round-to-nearest-credit billing.
// Nonnegative verified micros only; integer quotient/remainder avoids float loss
// and overflow from adding half a credit to a near-MaxInt64 value.
func videoChargeCredits(micros int64) int64 {
	credits := micros / 10000
	if micros%10000 >= 5000 {
		credits++
	}
	return credits
}
