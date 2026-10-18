package service

// Independent contract probes for explicit reauthorization after a bounded run.
// Synthetic data only; these do not contact a provider or perform payment/refund.
import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func independentExhaustedFixture(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	svc, db, oldRun := reviewOrchestratorFixture(t, "SHOTS_REVIEW")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg',spent_credits=200,total_seconds=10 WHERE id='review-p';
 INSERT INTO shots(id,project_id,shot_code,seq,duration,status,retry_count,upstream_id,video_url,delivered_upstream_id)
 VALUES('ind-failed','review-p','S01',1,5,'FAILED',2,'ind-terminal-task',NULL,NULL),
 ('ind-done','review-p','S02',2,5,'APPROVED',1,'ind-delivered-task','synthetic.mp4','ind-delivered-task')`)
	if _, err := db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,attempts,upstream_id,reserved_credits,run_id)
 VALUES('ind-old-job','shot','review-p','ind-failed',1,'failed','provider_failed',1,'ind-terminal-task',0,$1)`, oldRun); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id=$1 AND status='failed' AND waiting_reason='shot_failed'`, oldRun); n != 1 {
		t.Fatal("old authorization did not stop at its retry limit")
	}
	return svc, db, oldRun
}

func TestIndependentExplicitReauthorizationRearmsOnlyKnownFailedShot(t *testing.T) {
	svc, db, oldRun := independentExhaustedFixture(t)
	req := &StartRunReq{IdempotencyKey: "independent-explicit-retry"}
	start, err := svc.StartRun("review-u", "review-p", req)
	if err != nil {
		t.Fatal(err)
	}
	if start.Run.ID == oldRun {
		t.Fatal("explicit new authorization reused the failed run")
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	if err := db.QueryRow(`SELECT status,waiting_reason FROM generation_runs WHERE id=$1`, start.Run.ID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND kind='shot' AND shot_id='ind-failed' AND status='queued'`, start.Run.ID); n != 1 {
		t.Fatalf("explicit fresh authorization could not retry confirmed failure: new run=%s/%s jobs=%d", state, reason, n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND shot_id='ind-done'`, start.Run.ID); n != 0 {
		t.Fatal("reauthorization regenerated delivered footage")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE id='ind-old-job' AND run_id=$1 AND status='failed' AND phase='provider_failed' AND upstream_id='ind-terminal-task' AND reserved_credits=0`, oldRun); n != 1 {
		t.Fatal("old terminal task identity was rewritten")
	}
	if n := countRows(t, db, `SELECT count(*) FROM projects WHERE id='review-p' AND spent_credits=200`); n != 1 {
		t.Fatal("reauthorization changed previously settled credits")
	}
	var retry int
	if err := db.QueryRow(`SELECT retry_count FROM shots WHERE id='ind-failed'`).Scan(&retry); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.StartRun("review-u", "review-p", req)
	if err != nil || replay.Run.ID != start.Run.ID {
		t.Fatalf("same intent did not reuse new run: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM shots WHERE id='ind-failed' AND retry_count=$1`, retry); n != 1 {
		t.Fatal("idempotent replay reset the active attempt counter")
	}
	// An active run replay must not create a fresh retry allowance, even with a
	// different request key. The same authorized run still stops at ShotMaxRetry.
	mustExec(t, db, `UPDATE shots SET retry_count=2,status='FAILED',upstream_id='ind-new-terminal' WHERE id='ind-failed'`)
	if _, err := db.Exec(`UPDATE jobs SET status='failed',phase='provider_failed',upstream_id='ind-new-terminal',reserved_credits=0 WHERE run_id=$1`, start.Run.ID); err != nil {
		t.Fatal(err)
	}
	active, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "independent-active-replay"})
	if err != nil || active.Run.ID != start.Run.ID {
		t.Fatalf("active run was replaced: %v", err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id=$1 AND status='failed' AND waiting_reason='shot_failed'`, start.Run.ID); n != 1 {
		t.Fatal("same authorized run escaped its retry limit")
	}
	if _, err := svc.StartRun("review-u", "review-p", req); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM shots WHERE id='ind-failed' AND status='FAILED' AND retry_count=2`); n != 1 {
		t.Fatal("replaying terminal idempotency key reset exhausted counter")
	}
}

func TestIndependentExplicitReauthorizationRearmsKnownUnsubmittedFailure(t *testing.T) {
	svc, db, _ := independentExhaustedFixture(t)
	mustExec(t, db, `UPDATE jobs SET phase='ready',upstream_id=NULL WHERE id='ind-old-job';
 UPDATE shots SET upstream_id=NULL WHERE id='ind-failed'`)
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "independent-unsubmitted-retry"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND shot_id='ind-failed' AND status='queued'`, start.Run.ID); n != 1 {
		t.Fatal("new authorization did not retry provably unsubmitted failure")
	}
}

func TestIndependentExplicitReauthorizationRejectsUnprovedTerminalFailure(t *testing.T) {
	for _, state := range []string{"mismatched_identity", "reservation", "pending_receipt", "unknown_identity"} {
		t.Run(state, func(t *testing.T) {
			svc, db, _ := independentExhaustedFixture(t)
			switch state {
			case "mismatched_identity":
				mustExec(t, db, `UPDATE shots SET upstream_id='another-task' WHERE id='ind-failed'`)
			case "reservation":
				mustExec(t, db, `UPDATE jobs SET reserved_credits=200 WHERE id='ind-old-job'`)
			case "pending_receipt":
				mustExec(t, db, `INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url)
 VALUES('limeng','video','ind-terminal-task','review-p','ind-failed','review-u',2000000,200,'pending','synthetic.mp4')`)
			case "unknown_identity":
				mustExec(t, db, `UPDATE jobs SET upstream_id=NULL WHERE id='ind-old-job'; UPDATE shots SET upstream_id=NULL WHERE id='ind-failed'`)
			}
			start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "independent-keep-unproved"})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
				t.Fatal(err)
			}
			if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1`, start.Run.ID); n != 0 {
				t.Fatal("new authorization replaced unresolved task")
			}
			if n := countRows(t, db, `SELECT count(*) FROM shots WHERE id='ind-failed' AND status='FAILED' AND retry_count=2`); n != 1 {
				t.Fatal("new authorization reset unproved failure")
			}
		})
	}
}

func TestIndependentReauthorizationKeepsUncertainOriginalWork(t *testing.T) {
	for _, state := range []string{"submitting", "polling", "pending_receipt"} {
		t.Run(state, func(t *testing.T) {
			svc, db, oldRun := independentExhaustedFixture(t)
			phase, task, code := "submitting", "", "submission_unknown"
			if state != "submitting" {
				phase, task, code = "polling", "ind-terminal-task", "recovery_required"
			}
			if _, err := db.Exec(`UPDATE jobs SET status='reconcile',phase=$1,upstream_id=NULLIF($2,''),reserved_credits=200,error_code=$3 WHERE id='ind-old-job';`, phase, task, code); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE shots SET status='QUEUED',upstream_id=NULLIF($1,'') WHERE id='ind-failed'`, task); err != nil {
				t.Fatal(err)
			}
			if state == "pending_receipt" {
				mustExec(t, db, `INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url)
 VALUES('limeng','video','ind-terminal-task','review-p','ind-failed','review-u',2000000,200,'pending','synthetic.mp4')`)
			}
			start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "independent-keep-uncertain"})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
				t.Fatal(err)
			}
			if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1`, start.Run.ID); n != 0 {
				t.Fatal("reauthorization created replacement work for unresolved task")
			}
			if n := countRows(t, db, `SELECT count(*) FROM shots WHERE id='ind-failed' AND retry_count=2 AND status='QUEUED'`); n != 1 {
				t.Fatal("reauthorization reset unresolved attempt")
			}
			if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE id='ind-old-job' AND run_id=$1 AND phase=$2 AND COALESCE(upstream_id,'')=$3 AND reserved_credits=200`, oldRun, phase, task); n != 1 {
				t.Fatal("reauthorization changed original unknown/polling reservation")
			}
		})
	}
}

func TestIndependentProviderFailureCrashCanFinishAfterCancellation(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "SHOTS_GENERATING")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='review-p';
 INSERT INTO shots(id,project_id,shot_code,seq,duration,status,retry_count,upstream_id)
 VALUES('ind-crash-shot','review-p','S01',1,5,'FAILED',1,'ind-crash-task')`)
	// Represents process loss after processJob has durably recorded provider_failed
	// and FAILED but before FailJob commits terminal release of the reservation.
	if _, err := db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,run_id,heartbeat_at)
 VALUES('ind-crash-job','shot','review-p','ind-crash-shot',1,'running','provider_failed','ind-crash-task',200,$1,now()-interval '1 hour')`, oldRun); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelRun("review-u", "review-p", oldRun); err != nil {
		t.Fatal(err)
	}
	svc.ReclaimStaleJobs(time.Second)
	jobs, err := svc.ClaimJobs(JobShot, 10)
	if err != nil {
		t.Fatal(err)
	}
	var status, phase string
	var reserved int64
	if err := db.QueryRow(`SELECT status,phase,reserved_credits FROM jobs WHERE id='ind-crash-job'`).Scan(&status, &phase, &reserved); err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 && reserved != 0 {
		t.Fatalf("known provider failure stranded by cancelled run after crash: job=%s/%s reserved=%d claims=0", status, phase, reserved)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("LIMENG_BASE_URL", server.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	for _, job := range jobs {
		if err := svc.ExecuteJob(job); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("already durable provider failure contacted upstream again")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE id='ind-crash-job' AND status='failed' AND phase='provider_failed' AND upstream_id='ind-crash-task' AND reserved_credits=0`); n != 1 {
		t.Fatal("confirmed failed task did not release reservation exactly once")
	}
	if n := countRows(t, db, `SELECT count(*) FROM users WHERE id='review-u' AND credits=10000`); n != 1 {
		t.Fatal("confirmed failed task changed wallet balance")
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions`); n != 0 {
		t.Fatal("confirmed failed task wrote a financial ledger entry")
	}
}

func TestIndependentRepeatedCancellationKeepsSuccessorAndOriginalWork(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelRun("review-u", "review-p", oldRun); err != nil {
		t.Fatal(err)
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "independent-after-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelRun("review-u", "review-p", oldRun); err != nil {
		t.Fatalf("same owner/project/run cancellation replay failed: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id=$1 AND status='running'`, start.Run.ID); n != 1 {
		t.Fatal("old cancellation changed successor authorization")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND status='queued' AND reserved_credits=30`, start.Run.ID); n != 1 {
		t.Fatal("old cancellation changed successor job or reservation")
	}
	if err := svc.CancelRun("foreign-user", "review-p", oldRun); err == nil {
		t.Fatal("idempotent cancellation accepted another actor")
	}
	if err := svc.CancelRun("review-u", "another-project", oldRun); err == nil {
		t.Fatal("idempotent cancellation accepted another project")
	}
}

func TestIndependentResumeIdentifiedTaskIsIdempotentAndKeepsCancelledRun(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SHOTS_GENERATING")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='review-p';
 INSERT INTO shots(id,project_id,shot_code,seq,duration,status,retry_count,upstream_id)
 VALUES('ind-resume-shot','review-p','S01',1,5,'QUEUED',1,'ind-resume-task')`)
	if _, err := db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,attempts,run_id,error_code)
 VALUES('ind-resume-job','shot','review-p','ind-resume-shot',1,'reconcile','polling','ind-resume-task',200,6,$1,'recovery_required')`, runID); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelRun("review-u", "review-p", runID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeJob("review-p", "ind-resume-job"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeJob("review-p", "ind-resume-job"); err != nil {
		t.Fatalf("lost resume response cannot be retried while original task is queued: %v", err)
	}
	j := claimOne(t, svc, JobShot)
	if err := svc.ResumeJob("review-p", "ind-resume-job"); err != nil {
		t.Fatalf("lost resume response cannot be retried while original task is running: %v", err)
	}
	var posts, originalQueries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		if r.Method == http.MethodGet && r.URL.Path == "/videos/ind-resume-task" {
			originalQueries.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ind-resume-task","status":"running"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("LIMENG_BASE_URL", server.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	if err := svc.ExecuteJob(j); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 || originalQueries.Load() != 1 {
		t.Fatalf("resume did not query only original task: posts=%d queries=%d", posts.Load(), originalQueries.Load())
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id=$1 AND status='cancelled'`, runID); n != 1 {
		t.Fatal("original-task recovery resurrected cancelled run")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE id='ind-resume-job' AND upstream_id='ind-resume-task' AND reserved_credits=200 AND attempts=6 AND status='queued'`); n != 1 {
		t.Fatal("resume changed original identity, reservation or failure allowance")
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions`); n != 0 {
		t.Fatal("poll recovery unexpectedly charged the wallet")
	}
	if err := svc.ResumeJob("other-project", "ind-resume-job"); err == nil {
		t.Fatal("resume accepted mismatched project")
	}
	for _, state := range []string{"submitting", "empty_identity", "submission_unknown"} {
		phase, task, code := "polling", "ind-resume-task", ""
		switch state {
		case "submitting":
			phase = "submitting"
		case "empty_identity":
			task = ""
		case "submission_unknown":
			code = "submission_unknown"
		}
		if _, err := db.Exec(`UPDATE jobs SET status='reconcile',phase=$1,upstream_id=NULLIF($2,''),error_code=$3 WHERE id='ind-resume-job'`, phase, task, code); err != nil {
			t.Fatal(err)
		}
		if err := svc.ResumeJob("review-p", "ind-resume-job"); !errors.Is(err, ErrSubmissionUnknown) {
			t.Fatalf("%s must remain blocked: %v", state, err)
		}
	}
}
