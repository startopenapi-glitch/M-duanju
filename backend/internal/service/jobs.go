package service

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobReconcile = "reconcile"
	JobCancelled = "cancelled"
	JobKeyVisual = "keyvisual"
	JobShot      = "shot"
	JobAssemble  = "assemble"
)

var ErrLeaseLost = errors.New("任务租约已失效")
var ErrSubmissionUnknown = errors.New("上游提交结果未知，需要人工核对；禁止重复提交")

type Job struct {
	RequiredCredits         int64     `json:"requiredCredits"`
	AvailableCredits        int64     `json:"availableCredits"`
	ShortfallCredits        int64     `json:"shortfallCredits"`
	RunAfter                time.Time `json:"runAfter"`
	CanResume               bool      `json:"canResume"`
	ManualRequired          bool      `json:"manualRequired"`
	InfrastructureAllowance int       `json:"infrastructureAllowance"`
	ID                      string    `json:"id"`
	Kind                    string    `json:"kind"`
	ProjectID               string    `json:"projectId"`
	ShotID                  string    `json:"shotId"`
	Seq                     int       `json:"seq"`
	Attempts                int       `json:"attempts"`
	MaxAttempts             int       `json:"maxAttempts"`
	UpstreamID              string    `json:"upstreamId"`
	Payload                 string    `json:"-"`
	LastError               string    `json:"lastError"`
	RunID                   string    `json:"runId,omitempty"`
	LeaseToken              int64     `json:"-"`
	Phase                   string    `json:"phase"`
	Status                  string    `json:"status"`
	ReservedCredits         int64     `json:"reservedCredits"`
	ErrorCode               string    `json:"errorCode"`
}

func (s *Service) ClaimJobs(kind string, limit int) ([]*Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	if err := s.cleanupCancelledQueuedJobs(limit); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`WITH picked AS (
   SELECT id FROM jobs j WHERE j.kind=$1 AND j.status='queued' AND j.run_after<=now()
     AND (j.phase<>'ready' OR EXISTS(SELECT 1 FROM projects p JOIN users u ON u.id=p.user_id WHERE p.id=j.project_id AND u.status='active'))
	     AND ((j.phase IN ('polling','delivered','provider_failed') AND COALESCE(j.upstream_id,'')<>'') OR j.run_id IS NULL OR EXISTS(SELECT 1 FROM generation_runs r
          WHERE r.id=j.run_id AND r.status IN ('queued','running') AND r.auto_resume))
   ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED)
 UPDATE jobs SET status='running',lease_token=lease_token+1,heartbeat_at=now(),updated_at=now()
 WHERE id IN(SELECT id FROM picked) RETURNING id,kind,project_id,COALESCE(shot_id,''),COALESCE(seq,0),attempts,max_attempts,COALESCE(upstream_id,''),payload::text,COALESCE(last_error,''),lease_token,phase,reserved_credits,error_code,COALESCE(run_id,'')`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Job{}
	for rows.Next() {
		j := &Job{Status: JobRunning}
		if err = rows.Scan(&j.ID, &j.Kind, &j.ProjectID, &j.ShotID, &j.Seq, &j.Attempts, &j.MaxAttempts, &j.UpstreamID, &j.Payload, &j.LastError, &j.LeaseToken, &j.Phase, &j.ReservedCredits, &j.ErrorCode, &j.RunID); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func leaseResult(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *Service) RenewJob(id string, token int64) error {
	return leaseResult(s.db.Exec(`UPDATE jobs SET heartbeat_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, id, token))
}
func (s *Service) FinishJob(id string, token int64, upstream string) error {
	return leaseResult(s.db.Exec(`UPDATE jobs SET status='done',phase='done',upstream_id=COALESCE(NULLIF($3,''),upstream_id),heartbeat_at=now(),updated_at=now() WHERE id=$1 AND lease_token=$2 AND status IN ('running','done') AND reserved_credits=0`, id, token, upstream))
}
func (s *Service) FailJob(id string, token int64, cause error, retryable bool) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var project, shot, kind string
	if err = tx.QueryRow(`SELECT project_id,COALESCE(shot_id,''),kind FROM jobs WHERE id=$1`, id).Scan(&project, &shot, &kind); err != nil {
		return false, err
	}
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return false, err
	}
	if shot != "" {
		var target string
		if err = tx.QueryRow(`SELECT id FROM shots WHERE id=$1 AND project_id=$2 FOR UPDATE`, shot, project).Scan(&target); err != nil {
			return false, err
		}
	}
	var attempts, max int
	var phase, upstream string
	if err = tx.QueryRow(`SELECT attempts,max_attempts,phase,COALESCE(upstream_id,'') FROM jobs WHERE id=$1 AND lease_token=$2 AND status='running' FOR UPDATE`, id, token).Scan(&attempts, &max, &phase, &upstream); errors.Is(err, sql.ErrNoRows) {
		return false, ErrLeaseLost
	} else if err != nil {
		return false, err
	}
	if phase == "provider_failed" || phase == "submitting" && upstream == "" {
		retryable = false
	}
	attempts++
	transient := IsTransientError(cause)
	ceiling := max
	if transient {
		ceiling += 4
	}
	retry := retryable && attempts < ceiling
	status, code := JobFailed, "content_failed"
	delay := time.Duration(attempts*attempts) * 30 * time.Second
	if transient {
		code = "infrastructure_retry"
		delay = time.Duration(attempts) * 2 * time.Minute
	}
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	if retry {
		status = JobQueued
	} else if upstream != "" && phase != "provider_failed" || phase == "submitting" {
		status = JobReconcile
		code = "recovery_required"
	}
	if phase == "submitting" && upstream == "" {
		code = "submission_unknown"
	}
	res, err := tx.Exec(`UPDATE jobs SET status=$3,attempts=$4,last_error=$5,error_code=$6,run_after=now()+$7::interval,reserved_credits=CASE WHEN $3::varchar='failed' THEN 0 ELSE reserved_credits END,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, id, token, status, attempts, truncate(cause.Error(), 500), code, fmt.Sprintf("%d seconds", int(delay.Seconds())))
	if err = leaseResult(res, err); err != nil {
		return false, err
	}
	if status == JobFailed {
		if kind == JobShot {
			err = settlementExecOne(tx, "terminal shot", `UPDATE shots SET status='FAILED',error_msg=$1,updated_at=now() WHERE id=$2 AND project_id=$3`, truncate(cause.Error(), 400), shot, project)
		} else {
			projectStatus := "FAILED"
			if kind == JobAssemble {
				projectStatus = "SHOTS_REVIEW"
			}
			// A failed legacy cross-phase job must not overwrite another active phase.
			expected := "KEYVISUAL_GENERATING"
			if kind == JobAssemble {
				expected = "ASSEMBLING"
			}
			_, err = tx.Exec(`UPDATE projects SET status=$1,error_msg=$2,updated_at=now() WHERE id=$3 AND status=$4`, projectStatus, truncate(cause.Error(), 400), project, expected)
		}
		if err != nil {
			return false, err
		}
	}
	return retry, tx.Commit()
}
func (s *Service) deferJob(j *Job) error {
	return leaseResult(s.db.Exec(`UPDATE jobs SET status='queued',run_after=now()+interval '5 seconds',updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, j.ID, j.LeaseToken))
}
func (s *Service) ReclaimStaleJobs(timeout time.Duration) int {
	res, err := s.db.Exec(`UPDATE jobs SET status=CASE WHEN phase='submitting' AND COALESCE(upstream_id,'')='' THEN 'reconcile' ELSE 'queued' END,
 error_code=CASE WHEN phase='submitting' AND COALESCE(upstream_id,'')='' THEN 'submission_unknown' ELSE 'lease_recovered' END,
 lease_token=lease_token+1,run_after=now(),updated_at=now()
 WHERE status='running' AND heartbeat_at<now()-$1::interval`, fmt.Sprintf("%d seconds", int(timeout.Seconds())))
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}
func (s *Service) CountJobs(projectID string) map[string]int {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT status,count(*) FROM jobs WHERE ($1='' OR project_id=$1) GROUP BY status`, projectID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			out[k] = n
		}
	}
	return out
}

// withJob creates an immutable per-execution scope; the shared service is never mutated.
func (s *Service) withJob(j *Job) *Service { cp := *s; jc := *j; cp.job = &jc; return &cp }
func (s *Service) checkLease() error {
	if s.job == nil {
		return nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM jobs WHERE id=$1 AND lease_token=$2 AND status='running'`, s.job.ID, s.job.LeaseToken).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

// Call only after user/project/shot locks; holds the job fence through commit.
func (s *Service) fenceTx(tx *sql.Tx, project, shot, task string) error {
	if s.job != nil {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM jobs WHERE id=$1 AND lease_token=$2 AND status='running' AND project_id=$3 AND COALESCE(shot_id,'')=$4 AND ($5='' OR upstream_id=$5) FOR UPDATE`, s.job.ID, s.job.LeaseToken, project, shot, task).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	// Legacy result application is allowed only when no queue owns this target.
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND COALESCE(shot_id,'')=$2 AND kind=$3 AND status IN ('queued','running','reconcile'))`, project, shot, map[bool]string{true: JobShot, false: JobKeyVisual}[shot != ""]).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrLeaseLost
	}
	return nil
}

// Only queued, provably unsubmitted work of a terminal run may be discarded.
// In-flight/unknown submissions retain their identity and reservation.
func (s *Service) cleanupCancelledQueuedJobs(limit int) error {
	rows, err := s.db.Query(`SELECT j.id,j.project_id FROM jobs j JOIN generation_runs r ON r.id=j.run_id
 WHERE j.status='queued' AND j.phase='ready' AND COALESCE(j.upstream_id,'')='' AND r.status IN ('cancelled','failed','completed')
 ORDER BY j.created_at LIMIT $1`, limit)
	if err != nil {
		return err
	}
	type candidate struct{ id, project string }
	list := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.project); err != nil {
			rows.Close()
			return err
		}
		list = append(list, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range list {
		if err = s.cleanupCancelledQueuedJob(c.id, c.project); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) cleanupCancelledQueuedJob(id, project string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "run-auth:"+project); err != nil {
		return err
	}
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	var kind, shot string
	err = tx.QueryRow(`UPDATE jobs j SET status='cancelled',reserved_credits=0,lease_token=lease_token+1,error_code='run_cancelled',last_error='已取消的执行未提交此任务',updated_at=now()
 WHERE j.id=$1 AND j.status='queued' AND j.phase='ready' AND COALESCE(j.upstream_id,'')=''
 AND EXISTS(SELECT 1 FROM generation_runs r WHERE r.id=j.run_id AND r.status IN ('cancelled','failed','completed'))
 RETURNING kind,COALESCE(shot_id,'')`, id).Scan(&kind, &shot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == JobShot {
		_, err = tx.Exec(`UPDATE shots SET status='PENDING',error_msg=NULL,updated_at=now() WHERE id=$1 AND project_id=$2 AND status='QUEUED' AND COALESCE(upstream_id,'')=''`, shot, project)
	} else {
		from, to := "KEYVISUAL_GENERATING", "SCRIPT_REVIEW"
		if kind == JobAssemble {
			from, to = "ASSEMBLING", "SHOTS_REVIEW"
		}
		_, err = tx.Exec(`UPDATE projects SET status=$3,updated_at=now() WHERE id=$1 AND status=$2 AND ($4<>'keyvisual' OR COALESCE(keyvisual_task_id,'')='') AND NOT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND kind=$4 AND status IN ('queued','running','reconcile'))`, project, from, to, kind)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
