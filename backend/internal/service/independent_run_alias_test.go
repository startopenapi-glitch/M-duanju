package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	runpkg "drama-platform/internal/run"
)

func TestIndependentActiveRunRequestKeySurvivesResponseLossAndTermination(t *testing.T) {
	for _, terminal := range []string{"failed", "completed", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
			req := &StartRunReq{IdempotencyKey: "independent-response-lost-alias"}
			first, err := svc.StartRun("review-u", "review-p", req)
			if err != nil || first.Run.ID != original {
				t.Fatalf("same scope did not reuse active run: %v", err)
			}
			// The client never receives this successful reuse. The run terminates
			// before the browser can replay exactly the same pending request key.
			if _, err := db.Exec(`UPDATE generation_runs SET status=$2 WHERE id=$1`, original, terminal); err != nil {
				t.Fatal(err)
			}
			replay, err := svc.StartRun("review-u", "review-p", req)
			if err != nil {
				t.Fatal(err)
			}
			if replay.Run.ID != original {
				t.Fatalf("lost active-run reuse response created replacement execution after %s: original=%s replay=%s", terminal, original, replay.Run.ID)
			}
			if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='review-p'`); n != 1 {
				t.Fatalf("same pending request created %d runs", n)
			}
		})
	}
}

func TestIndependentRunRequestAliasContextAndConflict(t *testing.T) {
	svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	req := &StartRunReq{IdempotencyKey: "independent-read-alias"}
	if _, err := svc.StartRun("review-u", "review-p", req); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"review-start", req.IdempotencyKey} {
		view, err := svc.RunReadContext(context.Background(), "review-u", RunContextQuery{ProjectID: "review-p", RequestKey: key})
		if err != nil || view == nil || view.Run.ID != original {
			t.Fatalf("request key %s cannot recover original run: view=%v err=%v", key, view, err)
		}
	}
	if _, err := db.Exec(`UPDATE generation_runs SET status='failed' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: req.IdempotencyKey, Scope: json.RawMessage(`{"instruction":"changed"}`)}); !errors.Is(err, runpkg.ErrIdempotencyConflict) {
		t.Fatalf("bound alias accepted changed scope: %v", err)
	}
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,total_seconds,status,budget_credits) VALUES('ind-alias-other','review-u','synthetic',5,'SCRIPT_REVIEW',10000)`)
	if _, err := svc.StartRun("review-u", "ind-alias-other", req); !errors.Is(err, runpkg.ErrIdempotencyConflict) {
		t.Fatalf("bound alias accepted another project: %v", err)
	}
	if _, err := svc.RunReadContext(context.Background(), "someone-else", RunContextQuery{ProjectID: "review-p", RequestKey: req.IdempotencyKey}); !errors.Is(err, ErrUIReadNotFound) {
		t.Fatalf("alias leaked to another account: %v", err)
	}
}

func TestIndependentConcurrentAliasesRetainOneRunAfterTermination(t *testing.T) {
	svc, db, original := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			r, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: fmt.Sprintf("ind-alias-%d", n)})
			if err != nil || r.Run.ID != original {
				t.Errorf("concurrent same-scope request did not reuse original: %v", err)
			}
		}(i)
	}
	workers.Wait()
	if _, err := db.Exec(`UPDATE generation_runs SET status='failed' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		r, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: fmt.Sprintf("ind-alias-%d", i)})
		if err != nil || r.Run.ID != original {
			t.Fatalf("alias %d lost original after terminal: %v", i, err)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='review-p'`); n != 1 {
		t.Fatalf("alias replay created %d runs", n)
	}
}

func TestIndependentConcurrentNewRunAliasesSurviveTermination(t *testing.T) {
	svc, db, _ := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,total_seconds,status,budget_credits) VALUES('ind-alias-concurrent','review-u','synthetic',5,'SCRIPT_REVIEW',10000)`)
	blocker, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	key := "run:ind-alias-concurrent"
	if _, err = blocker.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		result *StartRunResult
		err    error
	}
	answers := make(chan answer, 2)
	for _, requestKey := range []string{"ind-alias-new-a", "ind-alias-new-b"} {
		go func(k string) {
			r, e := svc.StartRun("review-u", "ind-alias-concurrent", &StartRunReq{IdempotencyKey: k})
			answers <- answer{r, e}
		}(requestKey)
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
			t.Fatal("concurrent creation requests did not reach project serialization")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	original := ""
	for i := 0; i < 2; i++ {
		a := <-answers
		if a.err != nil || a.result == nil {
			t.Fatalf("concurrent same-scope creation failed: %v", a.err)
		}
		if original == "" {
			original = a.result.Run.ID
		}
		if a.result.Run.ID != original {
			t.Fatal("concurrent requests created different runs")
		}
	}
	if _, err := db.Exec(`UPDATE generation_runs SET status='failed' WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	for _, requestKey := range []string{"ind-alias-new-a", "ind-alias-new-b"} {
		r, err := svc.StartRun("review-u", "ind-alias-concurrent", &StartRunReq{IdempotencyKey: requestKey})
		if err != nil || r.Run.ID != original {
			t.Fatalf("atomic-create alias %s did not retain original: %v", requestKey, err)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='ind-alias-concurrent'`); n != 1 {
		t.Fatalf("concurrent alias replay created %d runs", n)
	}
}
