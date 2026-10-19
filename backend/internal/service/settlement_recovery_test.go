package service

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestSettlementEveryWriteFailureKeepsPending(t *testing.T) {
	for _, target := range []struct{ name, table, operation, condition string }{
		{"debit", "users", "UPDATE OF credits", "true"},
		{"ledger", "credit_transactions", "INSERT", "true"},
		{"total", "projects", "UPDATE OF spent_credits", "true"},
		{"video", "shots", "UPDATE OF video_url", "true"},
		{"receipt", "generation_settlements", "UPDATE OF status", "NEW.status='settled'"},
		{"image", "projects", "UPDATE OF keyvisual_url", "true"},
	} {
		for _, skip := range []bool{false, true} {
			t.Run(target.name+map[bool]string{true: "_zero_rows", false: "_exception"}[skip], func(t *testing.T) {
				s, db, in := settlementFixture(t)
				if target.name == "image" {
					mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='image-1' WHERE id='p'`)
					in.Kind = "image"
					in.TaskID = "image-1"
					in.ShotID = ""
				}
				action := `RAISE EXCEPTION 'synthetic write failure';`
				if skip {
					action = `RETURN NULL;`
				}
				mustExec(t, db, `CREATE FUNCTION injected_write() RETURNS trigger AS $$ BEGIN IF `+target.condition+` THEN `+action+` END IF; RETURN NEW; END $$ LANGUAGE plpgsql; CREATE TRIGGER injected_write BEFORE `+target.operation+` ON `+target.table+` FOR EACH ROW EXECUTE PROCEDURE injected_write()`)
				if ok, e := s.SettleGeneration(in); e == nil || ok {
					t.Fatalf("write failure returned success: %v %v", ok, e)
				}
				assertSettlementTotals(t, db, 1000, 0, 0)
				var state, reason, detail, url string
				if e := db.QueryRow(`SELECT status,COALESCE(pending_reason,''),COALESCE(pending_error,''),result_url FROM generation_settlements WHERE upstream_id=$1`, in.TaskID).Scan(&state, &reason, &detail, &url); e != nil {
					t.Fatal(e)
				}
				if state != "pending" || reason != "write" || detail == "" || url != in.URL {
					t.Fatalf("pending facts missing: %s %s %q %s", state, reason, detail, url)
				}
				var task string
				if in.Kind == "image" {
					db.QueryRow(`SELECT keyvisual_task_id FROM projects WHERE id='p'`).Scan(&task)
				} else {
					db.QueryRow(`SELECT upstream_id FROM shots WHERE id='s'`).Scan(&task)
				}
				if task != in.TaskID {
					t.Fatal("failed settlement lost task identity")
				}
				mustExec(t, db, `DROP TRIGGER injected_write ON `+target.table)
				if ok, e := s.SettleGeneration(in); e != nil || !ok {
					t.Fatalf("pending recovery failed: %v %v", ok, e)
				}
				cost := int64(200)
				if in.Kind == "image" {
					cost = 30
				}
				assertSettlementTotals(t, db, 1000-cost, cost, 1)
			})
		}
	}
}

func TestSettlementCommitFailureDoesNotClaimPending(t *testing.T) {
	s, db, in := settlementFixture(t)
	mustExec(t, db, `CREATE FUNCTION fail_commit() RETURNS trigger AS $$ BEGIN IF NEW.status='settled' THEN RAISE EXCEPTION 'synthetic deferred commit failure'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql; CREATE CONSTRAINT TRIGGER fail_commit AFTER UPDATE ON generation_settlements DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE PROCEDURE fail_commit()`)
	if ok, e := s.SettleGeneration(in); e == nil || ok || !strings.Contains(e.Error(), "commit") {
		t.Fatalf("commit failure returned %v %v", ok, e)
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	var count int
	var task, status string
	if e := db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements),upstream_id,status FROM shots WHERE id='s'`).Scan(&count, &task, &status); e != nil {
		t.Fatal(e)
	}
	if count != 0 || task != in.TaskID || status != "QUEUED" {
		t.Fatalf("commit failure claimed persistence or lost recovery identity: %d %s %s", count, task, status)
	}
}

func TestSettlementAllResultFactsImmutable(t *testing.T) {
	for _, pending := range []bool{true, false} {
		for _, field := range []string{"url", "duration", "width", "height", "amount", "project", "shot"} {
			t.Run(field+map[bool]string{true: "_pending", false: "_settled"}[pending], func(t *testing.T) {
				s, db, in := settlementFixture(t)
				if pending {
					mustExec(t, db, `UPDATE users SET credits=100 WHERE id='u'`)
				}
				_, e := s.SettleGeneration(in)
				if !pending && e != nil {
					t.Fatal(e)
				}
				mustExec(t, db, `UPDATE users SET credits=1000 WHERE id='u'`)
				changed := in
				switch field {
				case "url":
					changed.URL += "?other"
				case "duration":
					changed.Duration++
				case "width":
					changed.Width++
				case "height":
					changed.Height++
				case "amount":
					changed.ChargeMicros++
				case "project":
					mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('p2','u','synthetic',1000,'SHOTS_GENERATING'); INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id) VALUES('s2','p2','S01',1,'QUEUED','task-1')`)
					changed.ProjectID = "p2"
					changed.ShotID = "s2"
				case "shot":
					mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id) VALUES('s2','p','S02',2,'QUEUED','task-1')`)
					changed.ShotID = "s2"
				}
				if ok, e := s.SettleGeneration(changed); e == nil || ok {
					t.Fatalf("changed %s accepted: %v %v", field, ok, e)
				}
				var url string
				var duration float64
				var w, h int
				if e := db.QueryRow(`SELECT result_url,result_duration,result_width,result_height FROM generation_settlements WHERE upstream_id='task-1'`).Scan(&url, &duration, &w, &h); e != nil {
					t.Fatal(e)
				}
				if url != in.URL || duration != in.Duration || w != in.Width || h != in.Height {
					t.Fatal("immutable receipt changed")
				}
				spent, ledgers := int64(200), 1
				if pending {
					spent, ledgers = 0, 0
				}
				assertSettlementTotals(t, db, 1000, spent, ledgers)
			})
		}
	}
}

func TestLegacyMigrationProtectsKnownDeliveredTasks(t *testing.T) {
	_, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE shots SET status='GENERATED',video_url='old.mp4' WHERE id='s'; INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id,video_url) VALUES('a','p','S02',2,'APPROVED','old-approved','approved.mp4'),('f','p','S03',3,'FAILED','old-delivered','delivered.mp4'),('unknown','p','S04',4,'GENERATED',NULL,'unknown.mp4'); UPDATE shots SET delivered_upstream_id='old-delivered' WHERE id='f'; UPDATE projects SET status='KEYVISUAL_REVIEW',keyvisual_task_id='old-image',keyvisual_url='old.jpg' WHERE id='p'; DROP TABLE generation_settlements`)
	migration, e := os.ReadFile("../../migrations/009_generation_settlements.sql")
	if e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, string(migration))
	var legacy, ledgers int
	if e := db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements WHERE status='legacy_unverified' AND credits=0 AND charge_micros=0),(SELECT count(*) FROM credit_transactions)`).Scan(&legacy, &ledgers); e != nil {
		t.Fatal(e)
	}
	if legacy != 3 || ledgers != 0 {
		t.Fatalf("legacy=%d fabricated ledgers=%d", legacy, ledgers)
	}
	var delivered string
	if e := db.QueryRow(`SELECT COALESCE(delivered_upstream_id,'') FROM shots WHERE id='s'`).Scan(&delivered); e != nil {
		t.Fatal(e)
	}
	if delivered != "task-1" {
		t.Fatalf("legacy artifact identity missing: %s", delivered)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, e := db.Exec(q); e != nil {
		t.Fatal(e)
	}
}

func TestRegenerateKeepsLegacyAcrossPreflightAndSubmitFailures(t *testing.T) {
	for _, status := range []string{"GENERATED", "APPROVED", "FAILED"} {
		for _, failure := range []string{"budget", "balance", "submit"} {
			t.Run(status+"_"+failure, func(t *testing.T) {
				s, db, in := settlementFixture(t)
				mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='p'; UPDATE shots SET status='`+status+`',video_url='old.mp4',delivered_upstream_id='task-1',duration=5 WHERE id='s'`)
				if failure == "budget" {
					mustExec(t, db, `UPDATE projects SET budget_credits=0 WHERE id='p'`)
				}
				if failure == "balance" {
					mustExec(t, db, `UPDATE users SET credits=0 WHERE id='u'`)
				}
				creates := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					creates++
					http.Error(w, `{"error":{"message":"synthetic create failure"}}`, 503)
				}))
				defer server.Close()
				t.Setenv("LIMENG_BASE_URL", server.URL)
				t.Setenv("LIMENG_API_KEY", "synthetic")
				t.Setenv("VIDEO_RESOLUTION", "720P")
				if failure == "submit" {
					if e := s.RegenerateShot("p", 1, false); e != nil {
						t.Fatal(e)
					}
					if e := s.ExecuteJob(claimOne(t, s, JobShot)); !errors.Is(e, ErrSubmissionUnknown) {
						t.Fatalf("unknown submission: %v", e)
					}
				} else if e := s.RegenerateShot("p", 1, false); e == nil {
					t.Fatal("expected regeneration rejection")
				}
				var gotStatus, url, task string
				var retry int
				if e := db.QueryRow(`SELECT status,video_url,COALESCE(upstream_id,''),retry_count FROM shots WHERE id='s'`).Scan(&gotStatus, &url, &task, &retry); e != nil {
					t.Fatal(e)
				}
				expectedStatus, expectedTask, expectedRetry := status, "task-1", 0
				if failure == "submit" {
					expectedStatus, expectedTask, expectedRetry = "QUEUED", "", 1
				}
				if gotStatus != expectedStatus || url != "old.mp4" || task != expectedTask || retry != expectedRetry {
					t.Fatalf("failed retry consumed old delivery: %s %s %s %d", gotStatus, url, task, retry)
				}
				expected := 0
				if failure == "submit" {
					expected = 1
				}
				if creates != expected {
					t.Fatalf("creates=%d want %d", creates, expected)
				}
				mustExec(t, db, `UPDATE users SET credits=1000 WHERE id='u'; UPDATE projects SET budget_credits=1000 WHERE id='p'`)
				if ok, e := s.SettleGeneration(in); e != nil || ok {
					t.Fatalf("old task charged: %v %v", ok, e)
				}
				assertSettlementTotals(t, db, 1000, 0, 0)
			})
		}
	}
}

func TestRegenerateAndOldCallbackOnlyChargeNewTask(t *testing.T) {
	s, db, old := settlementFixture(t)
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='p'; UPDATE shots SET status='GENERATED',video_url='old.mp4',duration=5 WHERE id='s'`)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `{"id":"new-task","status":"queued"}`)
	}))
	defer server.Close()
	t.Setenv("LIMENG_BASE_URL", server.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		e := s.RegenerateShot("p", 1, false)
		if e == nil {
			js, ce := s.ClaimJobs(JobShot, 1)
			e = ce
			if e == nil && len(js) == 1 {
				e = s.ExecuteJob(js[0])
			} else if e == nil {
				e = fmt.Errorf("no queued replacement")
			}
		}
		errs <- e
	}()
	<-entered
	go func() {
		defer wg.Done()
		ok, e := s.SettleGeneration(old)
		if ok {
			e = fmt.Errorf("old task charged")
		}
		errs <- e
	}()
	close(release)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var url, task string
	var retry int
	if e := db.QueryRow(`SELECT video_url,upstream_id,retry_count FROM shots WHERE id='s'`).Scan(&url, &task, &retry); e != nil {
		t.Fatal(e)
	}
	if url != "old.mp4" || task != "new-task" || retry != 1 {
		t.Fatalf("replacement state %s %s %d", url, task, retry)
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	newResult := old
	newResult.TaskID = "new-task"
	newResult.URL = "new.mp4"
	mustExec(t, db, `UPDATE jobs SET run_after=now()`)
	active := claimOne(t, s, JobShot)
	if ok, e := s.withJob(active).SettleGeneration(newResult); e != nil || !ok {
		t.Fatalf("new task failed: %v %v", ok, e)
	}
	if _, e := s.SettleGeneration(old); e != nil {
		t.Fatal(e)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}

func TestSettlementSameAccountAcrossProjectsSerializesBalance(t *testing.T) {
	s, db, a := settlementFixture(t)
	mustExec(t, db, `UPDATE users SET credits=300 WHERE id='u'; INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('p2','u','synthetic',1000,'SHOTS_GENERATING'); INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id) VALUES('s2','p2','S01',1,'QUEUED','task-2')`)
	b := a
	b.ProjectID = "p2"
	b.ShotID = "s2"
	b.TaskID = "task-2"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, in := range []GenerationSettlement{a, b} {
		wg.Add(1)
		go func(in GenerationSettlement) { defer wg.Done(); <-start; _, e := s.SettleGeneration(in); errs <- e }(in)
	}
	close(start)
	wg.Wait()
	close(errs)
	pending := 0
	for e := range errs {
		if e != nil {
			if !strings.Contains(e.Error(), "balance") {
				t.Fatal(e)
			}
			pending++
		}
	}
	var balance, total int64
	var ledgers, receipts int
	if e := db.QueryRow(`SELECT (SELECT credits FROM users WHERE id='u'),(SELECT sum(spent_credits) FROM projects),(SELECT count(*) FROM credit_transactions),(SELECT count(*) FROM generation_settlements)`).Scan(&balance, &total, &ledgers, &receipts); e != nil {
		t.Fatal(e)
	}
	if pending != 1 || balance != 100 || total != 200 || ledgers != 1 || receipts != 2 {
		t.Fatalf("pending=%d balance=%d total=%d ledgers=%d receipts=%d", pending, balance, total, ledgers, receipts)
	}
}
