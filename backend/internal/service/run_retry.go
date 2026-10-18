package service

import "database/sql"

// A new explicit authorization receives its own bounded retry allowance. Only
// the latest provably failed attempt may be rearmed; uncertain paid work remains
// attached to its original task, reservation and receipt. CreateWithSetup calls
// this once under run-auth -> user -> project locks, never on idempotent replays.
func rearmFailedShotsTx(tx *sql.Tx, project, runID string) error {
	_, err := tx.Exec(`WITH retryable AS (
 SELECT sh.id FROM shots sh
 JOIN generation_runs current ON current.id=$2 AND current.project_id=sh.project_id
 JOIN LATERAL (
   SELECT j.status,j.phase,j.upstream_id,j.reserved_credits,j.run_id
   FROM jobs j WHERE j.project_id=sh.project_id AND j.shot_id=sh.id AND j.kind='shot'
   ORDER BY j.created_at DESC,j.id DESC LIMIT 1
 ) prior_job ON true
 LEFT JOIN generation_runs prior_run ON prior_run.id=prior_job.run_id
 WHERE sh.project_id=$1 AND sh.status='FAILED' AND sh.retry_count >= $3
 AND prior_job.status='failed' AND prior_job.reserved_credits=0
 AND (prior_job.run_id IS NULL OR
   (prior_run.user_id=current.user_id AND prior_run.project_id=current.project_id
    AND prior_run.project_version<current.project_version
    AND prior_run.status IN ('failed','cancelled','completed')))
 AND ((prior_job.phase='ready' AND COALESCE(prior_job.upstream_id,'')='' AND COALESCE(sh.upstream_id,'')='')
   OR (prior_job.phase='provider_failed' AND COALESCE(prior_job.upstream_id,'')<>'' AND prior_job.upstream_id=sh.upstream_id))
 AND NOT EXISTS(SELECT 1 FROM jobs pending WHERE pending.project_id=sh.project_id AND pending.shot_id=sh.id
   AND (pending.status IN ('queued','running','reconcile') OR pending.reserved_credits<>0))
 AND NOT EXISTS(SELECT 1 FROM generation_settlements g WHERE g.project_id=sh.project_id AND g.shot_id=sh.id
   AND (g.status='pending' OR g.upstream_id=prior_job.upstream_id))
 ) UPDATE shots SET status='PENDING',retry_count=0,error_msg=NULL,updated_at=now()
 WHERE id IN(SELECT id FROM retryable)`, project, runID, ShotMaxRetry)
	return err
}
