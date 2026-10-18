package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"drama-platform/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Starts a fresh OS process on the parent's guarded random test schema. No real
// worker or production database is touched; exit 77 models process loss after a
// committed claim (no deferred cleanup), not merely a new in-memory Service.
func restartReviewProcess(t *testing.T, db *sql.DB, phase string, crash bool) {
	t.Helper()
	var schema string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestartReviewChild$")
	cmd.Env = append(os.Environ(), "DRAMA_RESTART_PHASE="+phase, "DRAMA_RESTART_SCHEMA="+schema)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Env = append(os.Environ(), "DRAMA_RESTART_PHASE="+phase, "DRAMA_RESTART_SCHEMA="+schema)
	out, err := cmd.CombinedOutput()
	if crash {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 77 {
			t.Fatalf("claim process did not crash at durable boundary: %v %s", err, out)
		}
	} else if err != nil {
		t.Fatalf("restart process: %v %s", err, out)
	}
}
func TestRestartReviewChild(t *testing.T) {
	phase := os.Getenv("DRAMA_RESTART_PHASE")
	if phase == "" {
		return
	}
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || cfg.Database != testutil.DatabaseName {
		t.Fatal("guarded test database required")
	}
	schema := os.Getenv("DRAMA_RESTART_SCHEMA")
	if !regexp.MustCompile(`^test_[a-f0-9]{16}$`).MatchString(schema) {
		t.Fatal("random test schema required")
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	var actual string
	if e := db.QueryRow(`SELECT current_database()`).Scan(&actual); e != nil || actual != testutil.DatabaseName {
		t.Fatal("database guard failed")
	}
	svc := New(db, "synthetic-test-secret")
	switch phase {
	case "claim-jobs":
		jobs, e := svc.ClaimJobs(JobShot, 10)
		if e != nil || len(jobs) != 3 {
			t.Fatalf("claims=%d err=%v", len(jobs), e)
		}
		os.Exit(77)
	case "claim-wakeup":
		ws, e := svc.runstore.ClaimDue(context.Background(), 10)
		if e != nil || len(ws) != 1 {
			t.Fatalf("wakeups=%d err=%v", len(ws), e)
		}
		os.Exit(77)
	case "recover-jobs":
		svc.RecoverStuckJobs()
		svc.ReclaimStaleJobs(time.Second)
		svc.ResetStaleRuns(context.Background(), time.Second)
		jobs, e := svc.ClaimJobs(JobShot, 10)
		if e != nil {
			t.Fatal(e)
		}
		for _, j := range jobs {
			if j.Phase != "provider_failed" {
				t.Fatal("restart attempted uncertain or unsubmitted work", j.ID, j.Phase)
			}
			if e := svc.ExecuteJob(j); e != nil {
				t.Fatal(e)
			}
		}
	case "recover-wakeup":
		if _, e := svc.ResumeFromWakeups(context.Background(), 10); e != nil {
			t.Fatal(e)
		}
	default:
		t.Fatal("unsupported synthetic restart phase")
	}
}
func TestProcessRestartPreservesUnknownAndReleasesKnownFailure(t *testing.T) {
	svc, db, run := reviewOrchestratorFixture(t, "SHOTS_GENERATING")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='review-p';
 INSERT INTO shots(id,project_id,shot_code,seq,duration,status,retry_count,upstream_id) VALUES
 ('restart-failed','review-p','F',1,5,'FAILED',1,'known-task'),
 ('restart-unknown','review-p','U',2,5,'QUEUED',1,NULL),
 ('restart-ready','review-p','R',3,5,'QUEUED',1,NULL)`)
	for _, v := range []struct{ id, phase, task string }{{"failed", "provider_failed", "known-task"}, {"unknown", "submitting", ""}, {"ready", "ready", ""}} {
		if _, e := db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,run_id) VALUES($1,'shot','review-p',$1,1,'queued',$2,NULLIF($3,''),200,$4)`, "restart-"+v.id, v.phase, v.task, run); e != nil {
			t.Fatal(e)
		}
	}
	restartReviewProcess(t, db, "claim-jobs", true)
	var oldLease int64
	if e := db.QueryRow(`SELECT lease_token FROM jobs WHERE id='restart-failed'`).Scan(&oldLease); e != nil {
		t.Fatal(e)
	}
	if e := svc.CancelRun("review-u", "review-p", run); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE jobs SET heartbeat_at=now()-interval '1 hour' WHERE status='running'`)
	restartReviewProcess(t, db, "recover-jobs", false)
	restartReviewProcess(t, db, "recover-jobs", false)
	for _, q := range []string{
		`SELECT count(*) FROM jobs WHERE id='restart-failed' AND status='failed' AND reserved_credits=0 AND upstream_id='known-task'`,
		`SELECT count(*) FROM jobs WHERE id='restart-unknown' AND status='reconcile' AND error_code='submission_unknown' AND reserved_credits=200 AND COALESCE(upstream_id,'')=''`,
		`SELECT count(*) FROM jobs WHERE id='restart-ready' AND status='cancelled' AND reserved_credits=0`,
		`SELECT count(*) FROM users WHERE id='review-u' AND credits=10000`,
	} {
		if n := countRows(t, db, q); n != 1 {
			t.Fatalf("restart invariant failed: %s", q)
		}
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions`); n != 0 {
		t.Fatal("restart created money entries")
	}
	if e := svc.RenewJob("restart-failed", oldLease); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("old process lease remained usable", e)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id=$1 AND status='cancelled'`, run); n != 1 {
		t.Fatal("cancelled run resurrected")
	}
}
func TestProcessRestartConsumesPaidWakeupExactlyOnce(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "resume"
		if cancelled {
			name = "cancel-before-restart"
		}
		t.Run(name, func(t *testing.T) {
			svc, db := resumeFixture(t)
			payAndSettle(t, svc, 100, "r1")
			restartReviewProcess(t, db, "claim-wakeup", true)
			if cancelled {
				if e := svc.CancelRun("u1", "p1", "r1"); e != nil {
					t.Fatal(e)
				}
			}
			mustExec(t, db, `UPDATE resume_wakeups SET claimed_at=now()-interval '1 hour' WHERE status='claimed'`)
			restartReviewProcess(t, db, "recover-wakeup", false)
			restartReviewProcess(t, db, "recover-wakeup", false)
			if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1'`); n != 1 {
				t.Fatalf("ledger=%d", n)
			}
			if n := countRows(t, db, `SELECT count(*) FROM users WHERE id='u1' AND credits=10000`); n != 1 {
				t.Fatal("wallet changed on recovery")
			}
			if n := countRows(t, db, `SELECT count(*) FROM resume_wakeups WHERE status='done'`); n != 1 {
				t.Fatalf("done wakeups=%d", n)
			}
			expected := 1
			if cancelled {
				expected = 0
			}
			if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != expected {
				t.Fatalf("queued jobs=%d want=%d", n, expected)
			}
			if !cancelled && countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1' AND run_id='r1' AND status='queued' AND kind='shot' AND phase='ready' AND reserved_credits>0 AND COALESCE(upstream_id,'')=''`) != 1 {
				t.Fatal("restarted job lost run identity, ready state or reservation")
			}
			if cancelled && countRows(t, db, `SELECT count(*) FROM generation_runs WHERE id='r1' AND status='cancelled'`) != 1 {
				t.Fatal("cancelled run resurrected")
			}
		})
	}
}
