package service

// Independent review regressions. These assert the requested contract and are
// expected to fail on the reviewed snapshot; provider calls use localhost only.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"drama-platform/internal/testutil"
)

func reviewOrchestratorFixture(t *testing.T, stage string) (*Service, *sql.DB, string) {
	t.Helper()
	t.Setenv("VIDEO_RESOLUTION", "720P")
	db := testutil.DB(t)
	svc := New(db, "review-synthetic-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status)
		VALUES('review-u','review@example.invalid','unused',10000,'active')`)
	if _, err := db.Exec(`INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status,aspect)
		VALUES('review-p','review-u','synthetic',10000,5,$1,'16:9')`, stage); err != nil {
		t.Fatal(err)
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "review-start"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, db, start.Run.ID
}

func TestReviewOrchestratorCancelQueuedJobBeforeProvider(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/images" {
			creates.Add(1)
			json.NewEncoder(w).Encode(map[string]string{"id": "review-image", "status": "queued"})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND phase='ready'`, runID); n != 1 {
		t.Fatalf("setup expected one unsubmitted job, got %d", n)
	}
	if err := svc.CancelRun("review-u", "review-p", runID); err != nil {
		t.Fatal(err)
	}
	jobs, err := svc.ClaimJobs(JobKeyVisual, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		_ = svc.ExecuteJob(job)
	}
	if calls := creates.Load(); calls != 0 {
		t.Fatalf("cancel acknowledged before worker claim, but %d new paid provider submission(s) happened", calls)
	}
}

func TestReviewOrchestratorCancelDuringScriptStopsNextPaidStage(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "DRAFT")
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]string{"content": syntheticScript}},
		}})
	}))
	defer srv.Close()
	defer unblock()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	done := make(chan error, 1)
	go func() { done <- svc.AdvanceRun(context.Background(), runID) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("advance ended before provider request: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("script request not observed")
	}
	if err := svc.CancelRun("review-u", "review-p", runID); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("advance did not finish")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='review-p'`); n != 0 {
		t.Fatalf("cancelled while script was in flight, but returning old AdvanceRun enqueued %d new paid job(s)", n)
	}
}

func TestReviewOrchestratorRunLinkFailureIsRecoverable(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	// Fault at the separate UPDATE after EnqueueJob has committed.
	mustExec(t, db, `CREATE FUNCTION review_reject_run_link() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic link write failure'; END $$`)
	mustExec(t, db, `CREATE TRIGGER review_run_link_failure BEFORE UPDATE OF run_id ON jobs
		FOR EACH ROW EXECUTE PROCEDURE review_reject_run_link()`)
	_ = svc.AdvanceRun(context.Background(), runID)
	mustExec(t, db, `DROP TRIGGER review_run_link_failure ON jobs`)
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='review-p' AND run_id IS NULL`); n != 0 {
		t.Fatalf("after transient link failure and recovery sweep, %d committed paid job(s) remain permanently unowned by a run", n)
	}
}

func TestReviewOrchestratorLegacyBudgetBlockedContinuation(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "BUDGET_EXCEEDED")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='review-p'`)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,duration,status)
		VALUES('review-s','review-p','S01',1,5,'PENDING')`)
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	if err := db.QueryRow(`SELECT status,waiting_reason FROM generation_runs WHERE id=$1`, runID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state == "failed" || countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='review-p'`) == 0 {
		t.Fatalf("explicit continuation with sufficient credits did not leave legacy budget stop: run=%s reason=%s", state, reason)
	}
}

func TestReviewOrchestratorReviseDoesNotDeliverOldVersionAsNew(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "COMPLETED")
	mustExec(t, db, `UPDATE projects SET final_url='old-version.mp4' WHERE id='review-p'`)
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{
		IdempotencyKey: "review-revise", Mode: "revise",
		Scope: json.RawMessage(`{"instruction":"change the ending","totalSeconds":10,"aspect":"9:16"}`),
	})
	if err != nil {
		// This fixture requests an unsupported shape and lacks reusable source shots.
		// Early rejection is correct; it must not create a rechargeable revision or mutate V1.
		if countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='review-p' AND mode='revise'`) != 0 {
			t.Fatal("rejected revision left an executable run")
		}
		var preserved string
		if e := db.QueryRow(`SELECT final_url FROM projects WHERE id='review-p'`).Scan(&preserved); e != nil || preserved != "old-version.mp4" {
			t.Fatalf("rejected revision changed V1: %s %v", preserved, e)
		}
		return
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	var state, finalURL string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id=$1`, start.Run.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT final_url FROM projects WHERE id='review-p'`).Scan(&finalURL); err != nil {
		t.Fatal(err)
	}
	if state == "completed" && finalURL == "old-version.mp4" && countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1`, start.Run.ID) == 0 {
		t.Fatalf("revision v%d was marked completed immediately with old final URL and no revision work", start.Run.ProjectVersion)
	}
}

func TestReviewOrchestratorReauthorizationRetriesFailedShotOnly(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "SHOTS_REVIEW")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='review-p'`)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,duration,status,retry_count)
		VALUES('review-s','review-p','S01',1,5,'FAILED',0)`)
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "review-reauthorized"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	if err := db.QueryRow(`SELECT status,waiting_reason FROM generation_runs WHERE id=$1`, start.Run.ID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state == "failed" || countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND kind='shot'`, start.Run.ID) != 1 {
		t.Fatalf("explicit new authorization cannot recover a failed shot below retry limit: run=%s reason=%s", state, reason)
	}
}
