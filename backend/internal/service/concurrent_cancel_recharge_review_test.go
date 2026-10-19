package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"drama-platform/internal/model"
)

func assertCancelledTopup(t *testing.T, svc *Service, db *sql.DB) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`SELECT count(*) FROM generation_runs WHERE id='r1' AND status='cancelled'`,
		`SELECT count(*) FROM users WHERE id='u1' AND credits=10000`,
		`SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='recharge' AND amount=10000`,
		`SELECT count(*) FROM resume_wakeups WHERE run_id='r1' AND status='done'`,
	} {
		if countRows(t, db, q) != 1 {
			t.Fatal("cancel/topup invariant", q)
		}
	}
	if countRows(t, db, `SELECT count(*) FROM jobs WHERE status IN ('queued','running','reconcile') OR reserved_credits<>0`) != 0 {
		t.Fatal("cancel left runnable/reserved work")
	}
	if countRows(t, db, `SELECT count(*) FROM credit_transactions`) != 1 {
		t.Fatal("unexpected deduction or duplicate credit")
	}
}

// Explicit database barriers force both serialization orders. All orders and
// balances are synthetic in a guarded random test schema; no payment endpoint.
func TestConcurrentCancelBeforeCreditCommit(t *testing.T) {
	svc, db := resumeFixture(t)
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 100, RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Rollback()
	var pid int
	if err = guard.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	// Match a wallet UPDATE lock, allowing unrelated audit foreign-key checks.
	if _, err = guard.Exec(`SELECT id FROM users WHERE id='u1' FOR NO KEY UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic barrier"})
	}()
	reviewWait(t, db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid)
	if err = svc.CancelRun("u1", "p1", "r1"); err != nil {
		t.Fatal(err)
	}
	if err = guard.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settlement did not finish")
	}
	assertCancelledTopup(t, svc, db)
}

func TestConcurrentCreditThenCancelBeforeDispatch(t *testing.T) {
	svc, db := resumeFixture(t)
	payAndSettle(t, svc, 100, "r1")
	guard, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Rollback()
	if _, err = guard.Exec(`SELECT pg_advisory_xact_lock(hashtext('run-auth:p1'))`); err != nil {
		t.Fatal(err)
	}
	cancelled := make(chan error, 1)
	resumed := make(chan error, 1)
	go func() { cancelled <- svc.CancelRun("u1", "p1", "r1") }()
	reviewWait(t, db, `SELECT count(*)>=1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid::bigint=(hashtext('run-auth:p1')::bigint & 4294967295)`)
	go func() { _, e := svc.ResumeFromWakeups(context.Background(), 10); resumed <- e }()
	reviewWait(t, db, `SELECT count(*)>=2 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid::bigint=(hashtext('run-auth:p1')::bigint & 4294967295)`)
	if err = guard.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []chan error{cancelled, resumed} {
		select {
		case err = <-c:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("race did not finish")
		}
	}
	assertCancelledTopup(t, svc, db)
}

func TestCancelAfterRechargeJobClaimNeverSubmits(t *testing.T) {
	svc, db := resumeFixture(t)
	_, creates := providerSubmitCounter(t)
	payAndSettle(t, svc, 100, "r1")
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	job := claimOne(t, svc, JobShot)
	if err := svc.CancelRun("u1", "p1", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ExecuteJob(job); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 0 {
		t.Fatal("cancelled task submitted to mock provider")
	}
	assertCancelledTopup(t, svc, db)
}
