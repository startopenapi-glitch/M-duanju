package service

import (
	"context"
	"drama-platform/internal/model"
	"github.com/google/uuid"
	"testing"
)

// Isolated schemas and synthetic local receipt/media only. No external calls.
func TestIndependentDisabledFundedReceiptStillRecovers(t *testing.T) {
	svc, db := round2PendingMoney(t, 440, 0)
	mustExec(t, db, `UPDATE users SET role='admin' WHERE id='admin'`)
	if _, e := svc.SetUserStatus("admin", "u1", "banned", "independent review", 1); e != nil {
		t.Fatal(e)
	}
	if e := svc.AdjustCredits("admin", &model.AdjustCreditsReq{UserID: "u1", Amount: 500, Note: "fund verified existing receipt", RequestID: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		svc.AdvanceActiveRuns(context.Background(), 10)
		if _, e := svc.ResumeFromWakeups(context.Background(), 10); e != nil {
			t.Fatal(e)
		}
	}
	jobs, e := svc.ClaimJobs(JobShot, 10)
	if e != nil {
		t.Fatal(e)
	}
	var run, job, receipt, status string
	var balance int64
	if e = db.QueryRow(`SELECT r.status,j.status,g.status,u.status,u.credits FROM generation_runs r JOIN jobs j ON j.run_id=r.id JOIN generation_settlements g ON g.upstream_id=j.upstream_id JOIN users u ON u.id=r.user_id WHERE r.id='r1'`).Scan(&run, &job, &receipt, &status, &balance); e != nil {
		t.Fatal(e)
	}
	t.Logf("after three recovery cycles: account=%s balance=%d run=%s job=%s receipt=%s claims=%d", status, balance, run, job, receipt, len(jobs))
	if len(jobs) != 1 {
		t.Fatalf("verified existing funded receipt must remain claimable while account disabled; got %d", len(jobs))
	}
	if e = svc.ExecuteJob(jobs[0]); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT status FROM generation_settlements WHERE upstream_id='r2money-video'`).Scan(&receipt); e != nil || receipt != "settled" {
		t.Fatalf("receipt=%s error=%v", receipt, e)
	}
	if e = db.QueryRow(`SELECT status FROM generation_runs WHERE id='r1'`).Scan(&run); e != nil || run != "waiting_credits" {
		t.Fatalf("disabled run resurrected: %s %v", run, e)
	}
}

func TestIndependentDisabledPollingJobCanSettle(t *testing.T) {
	svc, db := round2PendingMoney(t, 440, 0)
	mustExec(t, db, `UPDATE users SET role='admin' WHERE id='admin'`)
	mustExec(t, db, `UPDATE jobs SET status='queued',run_after=now() WHERE id='r2money-job'; UPDATE users SET credits=720 WHERE id='u1'`)
	if _, e := svc.SetUserStatus("admin", "u1", "banned", "independent review", 1); e != nil {
		t.Fatal(e)
	}
	jobs, e := svc.ClaimJobs(JobShot, 10)
	if e != nil || len(jobs) != 1 {
		t.Fatalf("polling claims=%d error=%v", len(jobs), e)
	}
	if e = svc.ExecuteJob(jobs[0]); e != nil {
		t.Fatal(e)
	}
	var receipt string
	if e = db.QueryRow(`SELECT status FROM generation_settlements WHERE upstream_id='r2money-video'`).Scan(&receipt); e != nil || receipt != "settled" {
		t.Fatalf("receipt=%s error=%v", receipt, e)
	}
}
