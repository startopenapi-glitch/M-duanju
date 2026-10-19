package service

// Independent review regressions. These tests use testutil's guarded isolated
// schema, synthetic local media and manual ledger approval only. No provider or
// payment-channel requests are made.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"drama-platform/internal/model"
)

func TestReviewMoneyMidRunShortfallMustRemainTotalRequirement(t *testing.T) {
	t.Setenv("VIDEO_RESOLUTION", "720P")
	svc, db := resumeFixture(t)
	mustExec(t, db, `UPDATE users SET credits=400 WHERE id='u1';
		DELETE FROM generation_runs WHERE id='r4';
		UPDATE generation_runs SET status='queued' WHERE id='r1';
		INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES
		('s2','p1','S02',2,5),('s3','p1','S03',3,5)`)
	if err := svc.AdvanceRun(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	view, err := svc.LatestRun("u1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("three pending 5s shots need 600: run=%s estimate=%d available=%d displayed shortfall=%d", view.Run.Status, view.Run.CreditsEstimated, view.AvailableCredits, view.ShortfallCredits)
	if view.Run.ID != "r1" || view.Run.Status != "waiting_credits" || view.AvailableCredits != 400 {
		t.Fatalf("fixture did not reach insufficient-funds wait: %#v", view)
	}
	if view.ShortfallCredits != 200 {
		t.Errorf("need 600 with 400 available: expected shortfall 200, got %d", view.ShortfallCredits)
	}

	// A partial 100-credit top-up must still show the true remaining 100,
	// without claiming that available funds already cover the next stage.
	payAndSettle(t, svc, 1, "r1")
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	view, err = svc.LatestRun("u1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after partial top-up: run=%s estimate=%d available=%d displayed shortfall=%d jobs=%d", view.Run.Status, view.Run.CreditsEstimated, view.AvailableCredits, view.ShortfallCredits, countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`))
	if view.Run.Status != "waiting_credits" || view.ShortfallCredits != 100 {
		t.Errorf("need 600 with 500 available: expected waiting_credits/shortfall 100, got %s/%d", view.Run.Status, view.ShortfallCredits)
	}
}

func TestReviewMoneyPendingSettlementTopUpMustResumeExistingJob(t *testing.T) {
	svc, db := rechargeFixture(t)
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(testEncoder(t)), "ffprobe"))
	mustExec(t, db, `UPDATE users SET credits=220 WHERE id='u1';
		UPDATE projects SET status='SHOTS_GENERATING' WHERE id='p1';
		UPDATE generation_runs SET status='running' WHERE id='r1';
		INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id,duration)
		VALUES('review-s1','p1','S01',1,'QUEUED','review-paid-video',5);
		INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,run_id)
		VALUES('review-pending-job','shot','p1','review-s1',1,'queued','polling','review-paid-video',200,'r1')`)
	j := claimOne(t, svc, JobShot)
	payload := syntheticVideo(t)
	digest := sha256.Sum256(payload)
	file := filepath.Join(svc.storageDir("videos"), fmt.Sprintf("%s-%x.mp4", generationArtifactKey(j.UpstreamID), digest))
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	in := GenerationSettlement{Kind: "video", TaskID: j.UpstreamID, ProjectID: "p1", ShotID: "review-s1", URL: svc.publicURL(file), ChargeMicros: 2400000, Duration: 5, Width: 1344, Height: 768}
	var pending *SettlementPendingError
	if _, err := svc.withJob(j).SettleGeneration(in); !errors.As(err, &pending) || pending.Reason != "balance" {
		t.Fatalf("expected durable pending receipt: balance 220 covers reservation 200, but actual cost is 240: %v", err)
	}
	// ExecuteJob recovers the actual local receipt, then follows its real
	// SettlementPendingError -> parkSettlement path; no network is required.
	if err := svc.ExecuteJob(j); !errors.As(err, &pending) {
		t.Fatalf("expected ExecuteJob to park the existing result for credit: %v", err)
	} else {
		t.Logf("ExecuteJob parked with: %v", err)
	}
	var runStatus, jobStatus, receiptStatus string
	if err := db.QueryRow(`SELECT r.status,j.status,g.status
		FROM generation_runs r JOIN jobs j ON j.run_id=r.id
		JOIN generation_settlements g ON g.upstream_id=j.upstream_id
		WHERE r.id='r1'`).Scan(&runStatus, &jobStatus, &receiptStatus); err != nil {
		t.Fatal(err)
	}
	t.Logf("after insufficient settlement: run=%s job=%s receipt=%s", runStatus, jobStatus, receiptStatus)

	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 10, RunID: "r1"})
	if err != nil {
		t.Errorf("recharge-and-continue must accept run whose verified result needs settlement: %v", err)
		// Still examine what happens if the user follows the general recharge
		// route after the run-bound endpoint refuses their top-up.
		o, err = svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 10})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic independent review"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		svc.AdvanceActiveRuns(context.Background(), 10)
	}
	var balance int64
	if err := db.QueryRow(`SELECT r.status,j.status,g.status,u.credits
		FROM generation_runs r JOIN jobs j ON j.run_id=r.id
		JOIN generation_settlements g ON g.upstream_id=j.upstream_id
		JOIN users u ON u.id=r.user_id WHERE r.id='r1'`).Scan(&runStatus, &jobStatus, &receiptStatus, &balance); err != nil {
		t.Fatal(err)
	}
	claimable, err := svc.ClaimJobs(JobShot, 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after credited top-up and 3 recovery cycles: balance=%d run=%s job=%s receipt=%s claimable=%d", balance, runStatus, jobStatus, receiptStatus, len(claimable))
	if receiptStatus != "settled" && len(claimable) == 0 {
		t.Errorf("top-up left verified result permanently unclaimable: run=%s job=%s receipt=%s balance=%d", runStatus, jobStatus, receiptStatus, balance)
	}
}

func TestReviewMoneyWakeupBeforeWaitingTransitionMustNotBeLost(t *testing.T) {
	t.Setenv("VIDEO_RESOLUTION", "720P")
	svc, db := resumeFixture(t)
	mustExec(t, db, `DELETE FROM generation_runs WHERE id='r4';
		UPDATE users SET credits=400 WHERE id='u1';
		INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES
		('s2','p1','S02',2,5),('s3','p1','S03',3,5)`)
	// A valid pending top-up can outlive a previous wakeup of the same run.
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 2, RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE generation_runs SET status='queued' WHERE id='r1'`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	guard, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var namespace int64
	if err := guard.QueryRowContext(ctx, `SELECT hashtext(current_schema() || ':review-money-wakeup') & 2147483647`).Scan(&namespace); err != nil {
		guard.Close()
		t.Fatal(err)
	}
	if _, err := guard.ExecContext(ctx, `SELECT pg_advisory_lock($1::integer,1)`, namespace); err != nil {
		guard.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	started, joined := false, false
	defer func() {
		cancel()
		guard.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1::integer,1)`, namespace)
		guard.Close()
		if started && !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("blocked advance did not exit after barrier release")
			}
		}
	}()
	// A statement trigger runs BEFORE the UPDATE takes any run-row lock.
	// Key 2 proves arrival; key 1 freezes the interval after queue preflight
	// committed and before MarkWaitingCredits makes its stale decision durable.
	mustExec(t, db, fmt.Sprintf(`CREATE FUNCTION review_money_pause_wait() RETURNS trigger AS $$
		BEGIN
		  IF position('waiting_credits' in current_query()) > 0
		     AND position('required_credits' in current_query()) > 0 THEN
		    PERFORM pg_advisory_xact_lock(%d,2);
		    PERFORM pg_advisory_xact_lock(%d,1);
		  END IF;
		  RETURN NULL;
		END $$ LANGUAGE plpgsql;
		CREATE TRIGGER review_money_wait_barrier BEFORE UPDATE ON generation_runs
		FOR EACH STATEMENT EXECUTE PROCEDURE review_money_pause_wait()`, namespace, namespace))
	started = true
	go func() { done <- svc.AdvanceRun(ctx, "r1") }()
	for {
		select {
		case err := <-done:
			joined = true
			t.Fatalf("advance exited before reaching waiting-state barrier: %v", err)
		default:
		}
		var arrived bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks
			WHERE locktype='advisory' AND classid::bigint=$1 AND objid=2 AND objsubid=2 AND granted
			AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`, namespace).Scan(&arrived); err != nil {
			t.Fatalf("waiting for explicit advisory arrival barrier: %v", err)
		}
		if arrived {
			break
		}
	}
	// The actual top-up is committed and another consumer handles its outbox
	// event while the run still reads running. This consumer must not discard it.
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic race review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResumeFromWakeups(ctx, 10); err != nil {
		t.Fatal(err)
	}
	var beforeRun, beforeWakeup string
	var balance int64
	if err := db.QueryRowContext(ctx, `SELECT r.status,w.status,u.credits FROM generation_runs r
		JOIN resume_wakeups w ON w.run_id=r.id JOIN users u ON u.id=r.user_id
		WHERE r.id='r1' AND w.order_id=$1`, o.ID).Scan(&beforeRun, &beforeWakeup, &balance); err != nil {
		t.Fatal(err)
	}
	t.Logf("before waiting update proceeds: run=%s wakeup=%s balance=%d (stage needs 600)", beforeRun, beforeWakeup, balance)
	if _, err := guard.ExecContext(ctx, `SELECT pg_advisory_unlock($1::integer,1)`, namespace); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		joined = true
		t.Fatal(err)
	}
	joined = true
	mustExec(t, db, `DROP TRIGGER review_money_wait_barrier ON generation_runs`)
	// Exercise every existing rescue mechanism; none may require browser input.
	for i := 0; i < 3; i++ {
		if _, err := svc.ResumeFromWakeups(ctx, 10); err != nil {
			t.Fatal(err)
		}
		svc.ResetStaleRuns(ctx, 0)
		svc.AdvanceActiveRuns(ctx, 10)
	}
	var runStatus, wakeupStatus string
	if err := db.QueryRowContext(ctx, `SELECT r.status,w.status FROM generation_runs r
		JOIN resume_wakeups w ON w.run_id=r.id WHERE r.id='r1' AND w.order_id=$1`, o.ID).Scan(&runStatus, &wakeupStatus); err != nil {
		t.Fatal(err)
	}
	jobs := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`)
	t.Logf("after waiting update + 3 backfill/reclaim/stale-run/advance cycles: run=%s wakeup=%s jobs=%d balance=%d", runStatus, wakeupStatus, jobs, balance)
	if runStatus == "waiting_credits" && wakeupStatus == "done" && jobs == 0 {
		t.Fatal("credited balance covers stage but wakeup was consumed before waiting transition; existing recovery never resumes it")
	}
}
