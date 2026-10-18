package service

import (
	"context"
	"errors"
	"testing"
	"time"

	runpkg "drama-platform/internal/run"
)

func TestIndependentRunRequestMappingFailureRollsBackCreation(t *testing.T) {
	svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	mustExec(t, db, `UPDATE generation_runs SET status='failed'`)
	mustExec(t, db, `CREATE FUNCTION reject_review_run_request() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic request persistence failure'; END $$;
 CREATE TRIGGER reject_review_run_request BEFORE INSERT ON generation_run_requests FOR EACH ROW EXECUTE PROCEDURE reject_review_run_request()`)
	req := &StartRunReq{IdempotencyKey: "ind-alias-rollback-new"}
	if r, err := svc.StartRun("review-u", "review-p", req); err == nil || r != nil {
		t.Fatalf("mapping failure reported success: run=%v err=%v", r, err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='review-p'`); n != 1 {
		t.Fatalf("mapping failure left %d runs", n)
	}
	mustExec(t, db, `DROP TRIGGER reject_review_run_request ON generation_run_requests`)
	result, err := svc.StartRun("review-u", "review-p", req)
	if err != nil || result.Run.ID == original {
		t.Fatalf("same request could not safely retry after proven rollback: %v", err)
	}
	view, err := svc.RunReadContext(context.Background(), "review-u", RunContextQuery{ProjectID: "review-p", RequestKey: req.IdempotencyKey})
	if err != nil || view == nil || view.Run.ID != result.Run.ID {
		t.Fatalf("created run lacks committed request mapping: %v", err)
	}
}

func TestIndependentRunRequestMappingFailureDoesNotAcknowledgeReuse(t *testing.T) {
	svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	mustExec(t, db, `CREATE FUNCTION reject_review_run_request() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic request persistence failure'; END $$;
 CREATE TRIGGER reject_review_run_request BEFORE INSERT ON generation_run_requests FOR EACH ROW EXECUTE PROCEDURE reject_review_run_request()`)
	req := &StartRunReq{IdempotencyKey: "ind-alias-rollback-reuse"}
	if r, err := svc.StartRun("review-u", "review-p", req); err == nil || r != nil {
		t.Fatalf("unpersisted alias acknowledged: run=%v err=%v", r, err)
	}
	mustExec(t, db, `DROP TRIGGER reject_review_run_request ON generation_run_requests`)
	result, err := svc.StartRun("review-u", "review-p", req)
	if err != nil || result.Run.ID != original {
		t.Fatalf("alias retry did not bind original active run: %v", err)
	}
	mustExec(t, db, `UPDATE generation_runs SET status='failed'`)
	result, err = svc.StartRun("review-u", "review-p", req)
	if err != nil || result.Run.ID != original {
		t.Fatalf("committed reuse alias lost after terminal: %v", err)
	}
}

func TestIndependentRunRequestKeysAreAccountScoped(t *testing.T) {
	svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	req := &StartRunReq{IdempotencyKey: "ind-account-alias"}
	first, err := svc.StartRun("review-u", "review-p", req)
	if err != nil || first.Run.ID != original {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES('ind-alias-user','alias@example.invalid','unused',10000,'active');
 INSERT INTO projects(id,user_id,idea,total_seconds,status,budget_credits) VALUES('ind-alias-project','ind-alias-user','synthetic',5,'SCRIPT_REVIEW',10000)`)
	second, err := svc.StartRun("ind-alias-user", "ind-alias-project", req)
	if err != nil || second.Run.ID == original {
		t.Fatalf("separate account cannot use own key: %v", err)
	}
	if _, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: req.IdempotencyKey, Mode: "revise"}); !errors.Is(err, runpkg.ErrIdempotencyConflict) {
		t.Fatalf("alias accepted changed mode: %v", err)
	}
	if _, err := svc.RunReadContext(context.Background(), "ind-alias-user", RunContextQuery{ProjectID: "review-p", RequestKey: req.IdempotencyKey}); !errors.Is(err, ErrUIReadNotFound) {
		t.Fatalf("cross-account alias exposed original: %v", err)
	}
}

func TestIndependentRunAliasBindsObservedRunAfterItEnds(t *testing.T) {
	svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	lock := "run:review-p"
	if _, err = blocker.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, lock); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		result *StartRunResult
		err    error
	}
	answers := make(chan answer, 1)
	req := &StartRunReq{IdempotencyKey: "ind-alias-terminal-race"}
	go func() { r, e := svc.StartRun("review-u", "review-p", req); answers <- answer{r, e} }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid::bigint=(hashtext($1)::bigint & 4294967295)`, lock).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("alias request did not reach bind transaction")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := db.Exec(`UPDATE generation_runs SET status='failed' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO generation_runs(id,user_id,project_id,project_version,mode,status,idempotency_key,input_hash) VALUES('11111111111111111111111111111111','review-u','review-p',2,'first','queued','ind-alias-successor','synthetic-other')`)
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	got := <-answers
	if got.err != nil || got.result.Run.ID != original {
		t.Fatalf("observed request rebound to successor: %v", got.err)
	}
	replay, err := svc.StartRun("review-u", "review-p", req)
	if err != nil || replay.Run.ID != original {
		t.Fatalf("terminal alias did not preserve observation: %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id='11111111111111111111111111111111' AND status='queued' AND project_version=2`); n != 1 {
		t.Fatal("binding original mutated successor")
	}
}

func TestIndependentConcurrentRunAliasKeyAcrossProjectsHasOneOwner(t *testing.T) {
	svc, db, _ := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,total_seconds,status,budget_credits) VALUES('ind-alias-race-project','review-u','synthetic',5,'SCRIPT_REVIEW',10000)`)
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	locks := []string{"run:review-p", "run:ind-alias-race-project"}
	for _, lock := range locks {
		if _, err = blocker.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, lock); err != nil {
			t.Fatal(err)
		}
	}
	type answer struct {
		result *StartRunResult
		err    error
	}
	answers := make(chan answer, 2)
	req := &StartRunReq{IdempotencyKey: "ind-alias-cross-project-key"}
	for _, project := range []string{"review-p", "ind-alias-race-project"} {
		go func(p string) { r, e := svc.StartRun("review-u", p, req); answers <- answer{r, e} }(project)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid::bigint IN ((hashtext($1)::bigint & 4294967295),(hashtext($2)::bigint & 4294967295),(hashtext($3)::bigint & 4294967295))`, locks[0], locks[1], "run-request:review-u:"+req.IdempotencyKey).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cross-project requests did not reach serialization")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	succeeded, conflicted := 0, 0
	var winner *StartRunResult
	for i := 0; i < 2; i++ {
		got := <-answers
		if got.err == nil && got.result != nil {
			succeeded++
			winner = got.result
		} else if errors.Is(got.err, runpkg.ErrIdempotencyConflict) {
			conflicted++
		} else {
			t.Fatalf("unexpected cross-project result: %v", got.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("same account/key yielded success=%d conflict=%d", succeeded, conflicted)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_run_requests WHERE user_id='review-u' AND request_key=$1 AND run_id=$2`, req.IdempotencyKey, winner.Run.ID); n != 1 {
		t.Fatal("request has no unique durable owner")
	}
	if _, err := db.Exec(`UPDATE generation_runs SET status='failed' WHERE id=$1`, winner.Run.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.StartRun("review-u", winner.Run.ProjectID, req)
	if err != nil || replay.Run.ID != winner.Run.ID {
		t.Fatalf("winning identity changed after terminal: %v", err)
	}
}
