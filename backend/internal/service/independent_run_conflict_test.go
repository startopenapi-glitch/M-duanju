package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	runpkg "drama-platform/internal/run"
)

func TestIndependentRunActiveRejectionIsDefinitelyNotCreated(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	result, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "independent-conflicting-run", Scope: json.RawMessage(`{"instruction":"different intent"}`)})
	var rejected *RunNotCreatedError
	if result != nil || !errors.Is(err, runpkg.ErrRunActive) || !errors.As(err, &rejected) {
		t.Fatalf("definite active conflict lacks not-created classification: result=%v err=%v", result, err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs`); n != 1 {
		t.Fatal("rejected request created another run")
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id=$1 AND status='queued'`, runID); n != 1 {
		t.Fatal("conflict changed the active run")
	}
}

func TestIndependentConcurrentDifferentRunIntentRejectsBeforeCreation(t *testing.T) {
	svc, db, _ := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,total_seconds,status,budget_credits) VALUES('independent-conflict-project','review-u','synthetic',5,'SCRIPT_REVIEW',10000)`)
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	key := "run:independent-conflict-project"
	if _, err = blocker.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		run *StartRunResult
		err error
	}
	answers := make(chan answer, 2)
	for _, scope := range []string{`{"instruction":"one"}`, `{"instruction":"two"}`} {
		go func(raw string) {
			r, e := svc.StartRun("review-u", "independent-conflict-project", &StartRunReq{IdempotencyKey: raw, Scope: json.RawMessage(raw)})
			answers <- answer{r, e}
		}(scope)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid::bigint=(hashtext($1)::bigint & 4294967295)`, key).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("both requests did not reach the atomic create boundary")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	succeeded, rejected := 0, 0
	for i := 0; i < 2; i++ {
		result := <-answers
		if result.err == nil && result.run != nil {
			succeeded++
			continue
		}
		var notCreated *RunNotCreatedError
		if errors.Is(result.err, runpkg.ErrRunActive) && errors.As(result.err, &notCreated) {
			rejected++
			continue
		}
		t.Fatalf("atomic active conflict lost definite-not-created fact: %v", result.err)
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("success=%d safe rejection=%d", succeeded, rejected)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='independent-conflict-project'`); n != 1 {
		t.Fatalf("concurrent distinct intents created %d runs", n)
	}
}
