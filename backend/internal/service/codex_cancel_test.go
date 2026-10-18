package service

import (
	"context"
	"drama-platform/internal/model"
	"errors"
	"testing"
	"time"
)

func TestCodexCancelledReceiptSettlesWithoutResurrectingRun(t *testing.T) {
	svc, db := round2PendingMoney(t, 240, 0)
	if err := svc.CancelRun("u1", "p1", "r1"); err != nil {
		t.Fatal(err)
	}
	payAndSettle(t, svc, 1, "")
	for i := 0; i < 3; i++ {
		svc.AdvanceActiveRuns(context.Background(), 10)
	}
	jobs, err := svc.ClaimJobs(JobShot, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("cancelled funded receipt must recover: claims=%d", len(jobs))
	}
	if err = svc.ExecuteJob(jobs[0]); err != nil {
		t.Fatal(err)
	}
	var st string
	var bal int64
	if err = db.QueryRow(`SELECT r.status,u.credits FROM generation_runs r JOIN users u ON u.id=r.user_id WHERE r.id='r1'`).Scan(&st, &bal); err != nil {
		t.Fatal(err)
	}
	if st != "cancelled" || bal != 80 {
		t.Fatalf("status=%s balance=%d", st, bal)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='spend'`); n != 1 {
		t.Fatal(n)
	}
}

func TestCodexLegalCentManualAmountsPreserved(t *testing.T) {
	svc, db := rechargeFixture(t)
	for _, cents := range []int64{1, 29, 99, 100, 101, 1990, 10000, 50000, 300000} {
		o, e := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: float64(cents) / 100, RunID: "r1"})
		if e != nil {
			t.Fatal(e)
		}
		var stored, credits int64
		if e = db.QueryRow(`SELECT amount_cents,credits FROM recharge_orders WHERE id=$1`, o.ID).Scan(&stored, &credits); e != nil {
			t.Fatal(e)
		}
		want := rechargeCredits(cents)
		if stored != cents || credits != want {
			t.Fatalf("cent contract changed: %d→%d/%d want credits %d", cents, stored, credits, want)
		}
	}
}

func TestReviewerCancelClaimCrashDoesNotStrandSuccessor(t *testing.T) {
	for _, kind := range []string{JobKeyVisual, JobShot, JobAssemble} {
		t.Run(kind, func(t *testing.T) {
			stage := "SCRIPT_REVIEW"
			if kind == JobShot {
				stage = "KEYVISUAL_REVIEW"
			}
			if kind == JobAssemble {
				stage = "SHOTS_REVIEW"
			}
			svc, db, old := reviewOrchestratorFixture(t, stage)
			if kind != JobKeyVisual {
				mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.png' WHERE id='review-p';INSERT INTO shots(id,project_id,seq,shot_code,duration,status) VALUES('shot','review-p',1,'S01',5,'PENDING')`)
			}
			if kind == JobAssemble {
				mustExec(t, db, `UPDATE shots SET status='APPROVED',video_url='http://127.0.0.1/unused.mp4' WHERE id='shot'`)
			}
			if e := svc.AdvanceRun(context.Background(), old); e != nil {
				t.Fatal(e)
			}
			j := claimOne(t, svc, kind)
			if e := svc.CancelRun("review-u", "review-p", old); e != nil {
				t.Fatal(e)
			}
			if _, e := db.Exec(`UPDATE jobs SET heartbeat_at=now()-interval '1 hour' WHERE id=$1`, j.ID); e != nil {
				t.Fatal(e)
			}
			svc.ReclaimStaleJobs(time.Minute)
			next, e := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "successor"})
			if e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 3; i++ {
				if e = svc.AdvanceRun(context.Background(), next.Run.ID); e != nil {
					t.Fatal(e)
				}
				claims, e := svc.ClaimJobs(kind, 10)
				if e != nil {
					t.Fatal(e)
				}
				if len(claims) > 0 {
					if len(claims) != 1 || claims[0].RunID != next.Run.ID {
						t.Fatal("claimed cancelled work instead of successor")
					}
					if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE id=$1 AND status='cancelled' AND reserved_credits=0`, j.ID); n != 1 {
						t.Fatal("old reservation not released")
					}
					return
				}
			}
			t.Fatal("cancelled claimed job stranded successor")
		})
	}
}

func TestReviewerSubmittedOldRunCannotDebitNewOwner(t *testing.T) {
	svc, db, run := reviewOrchestratorFixture(t, "SHOTS_GENERATING")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES('next-owner','next@example.invalid','unused',10000,'active');INSERT INTO shots(id,project_id,seq,shot_code,duration,status,upstream_id) VALUES('shot','review-p',1,'S01',5,'QUEUED','already-submitted')`)
	if _, e := db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,run_id) VALUES('job','shot','review-p','shot',1,'queued','polling','already-submitted',200,$1)`, run); e != nil {
		t.Fatal(e)
	}
	j := claimOne(t, svc, JobShot)
	mustExec(t, db, `UPDATE projects SET user_id='next-owner' WHERE id='review-p'`)
	_, e := svc.withJob(j).SettleGeneration(GenerationSettlement{Kind: "video", TaskID: "already-submitted", ProjectID: "review-p", ShotID: "shot", URL: "http://127.0.0.1/synthetic.mp4", ChargeMicros: 400000, Duration: 5, Width: 1344, Height: 768})
	var credits int64
	if e2 := db.QueryRow(`SELECT credits FROM users WHERE id='next-owner'`).Scan(&credits); e2 != nil {
		t.Fatal(e2)
	}
	if credits != 10000 {
		t.Fatalf("new owner charged for original run: credits=%d settlementErr=%v", credits, e)
	}
	var pending *SettlementPendingError
	if !errors.As(e, &pending) || pending.Reason != "ownership" {
		t.Fatalf("ownership conflict not retained for reconciliation: %v", e)
	}
	if err := svc.parkSettlement(j, pending); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE kind='spend'`); n != 0 {
		t.Fatal("unexpected debit")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE id='job' AND status='reconcile' AND reserved_credits=200`); n != 1 {
		t.Fatal("lost original outstanding task")
	}
}
