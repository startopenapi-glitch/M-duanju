package service

// Independent round-two review regressions. All data lives in testutil's guarded
// local schema. Existing receipt media is synthetic and local; no upstream POST.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"drama-platform/internal/model"
)

func round2PendingMoney(t *testing.T, receiptCredits, oldRequirement int64) (*Service, *sql.DB) {
	t.Helper()
	svc, db := rechargeFixture(t)
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(testEncoder(t)), "ffprobe"))
	t.Setenv("WXPAY_APPID", "")
	t.Setenv("WXPAY_MOCK", "0")
	t.Setenv("WXPAY_APIV3_KEY_FILE", "")
	resetPayRate()
	mustExec(t, db, `DELETE FROM generation_runs WHERE id='r4';
		UPDATE users SET credits=220 WHERE id='u1';
		UPDATE projects SET status='SHOTS_GENERATING' WHERE id='p1';
		UPDATE generation_runs SET status='running' WHERE id='r1';
		INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id,duration)
		VALUES('r2money-shot','p1','S01',1,'QUEUED','r2money-video',5);
		INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,run_id)
		VALUES('r2money-job','shot','p1','r2money-shot',1,'queued','polling','r2money-video',200,'r1')`)
	if _, err := db.Exec(`UPDATE generation_runs SET required_credits=$1 WHERE id='r1'`, oldRequirement); err != nil {
		t.Fatal(err)
	}
	j := claimOne(t, svc, JobShot)
	payload := syntheticVideo(t)
	digest := sha256.Sum256(payload)
	file := filepath.Join(svc.storageDir("videos"), fmt.Sprintf("%s-%x.mp4", generationArtifactKey(j.UpstreamID), digest))
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	in := GenerationSettlement{Kind: "video", TaskID: j.UpstreamID, ProjectID: "p1", ShotID: "r2money-shot",
		URL: svc.publicURL(file), ChargeMicros: receiptCredits * 10000, Duration: 5, Width: 1344, Height: 768}
	var pending *SettlementPendingError
	if _, err := svc.withJob(j).SettleGeneration(in); !errors.As(err, &pending) || pending.Reason != "balance" {
		t.Fatalf("need trusted balance-pending receipt, got %v", err)
	}
	if err := svc.ExecuteJob(j); !errors.As(err, &pending) || pending.Reason != "balance" {
		t.Fatalf("need real ExecuteJob park path, got %v", err)
	}
	return svc, db
}

func round2ManualPay(t *testing.T, svc *Service, cents int64) {
	t.Helper()
	o, _, _, downlevel, err := svc.CreatePayOrder("u1", cents, "native", "127.0.0.1", "r1")
	if err != nil || !downlevel || o == nil {
		t.Fatalf("local manual order: downlevel=%v order=%v err=%v", downlevel, o, err)
	}
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "round2 local synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
}

func round2AssertOriginalSettles(t *testing.T, svc *Service, db *sql.DB, wantBalance int64) {
	t.Helper()
	for i := 0; i < 3; i++ {
		svc.AdvanceActiveRuns(context.Background(), 10)
	}
	jobs, err := svc.ClaimJobs(JobShot, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.ID != "r2money-job" || j.UpstreamID != "r2money-video" {
			t.Fatalf("replaced original job/upstream: %#v", j)
		}
		if err := svc.ExecuteJob(j); err != nil {
			t.Fatal(err)
		}
	}
	var runStatus, reason, jobStatus, receiptStatus string
	var required, balance int64
	if err := db.QueryRow(`SELECT r.status,r.waiting_reason,r.required_credits,j.status,g.status,u.credits
		FROM generation_runs r JOIN jobs j ON j.run_id=r.id
		JOIN generation_settlements g ON g.upstream_id=j.upstream_id
		JOIN users u ON u.id=r.user_id WHERE r.id='r1'`).Scan(&runStatus, &reason, &required, &jobStatus, &receiptStatus, &balance); err != nil {
		t.Fatal(err)
	}
	view, err := svc.LatestRun("u1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	spends := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='spend'`)
	t.Logf("final: run=%s reason=%s required=%d available=%d shortfall=%d balance=%d job=%s receipt=%s claimable=%d spends=%d",
		runStatus, reason, required, view.AvailableCredits, view.ShortfallCredits, balance, jobStatus, receiptStatus, len(jobs), spends)
	if receiptStatus != "settled" || jobStatus != "done" || balance != wantBalance || spends != 1 {
		t.Errorf("funded original receipt must settle once: want settled/done, balance=%d, spends=1", wantBalance)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1' AND kind='shot'`); n != 1 {
		t.Errorf("expected exactly original shot job, got %d", n)
	}
}

func TestRound2MoneyPartialTopupKeepsSettlementRequirement(t *testing.T) {
	svc, db := round2PendingMoney(t, 440, 0)
	round2ManualPay(t, svc, 100) // 220 + 100 < 440: a legitimate partial top-up.
	view, err := svc.LatestRun("u1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after partial: reason=%s available=%d shortfall=%d", view.Run.WaitingReason, view.AvailableCredits, view.ShortfallCredits)
	if view.ShortfallCredits != 120 || view.Run.WaitingReason != "settlement" {
		t.Errorf("partial settlement must retain own-reservation exclusion: want settlement/shortfall120")
	}
	round2ManualPay(t, svc, 120) // Now 440 covers the full stored receipt.
	round2AssertOriginalSettles(t, svc, db, 0)
}

func TestRound2MoneySchedulerBeforeTopupKeepsSettlementReason(t *testing.T) {
	svc, db := round2PendingMoney(t, 240, 0)
	// Normal worker scanning while waiting must not change accounting semantics.
	svc.AdvanceActiveRuns(context.Background(), 10)
	round2ManualPay(t, svc, 100) // 320 covers original 240, with 80 remaining.
	round2AssertOriginalSettles(t, svc, db, 80)
}

func TestRound2MoneyOldStageRequirementDoesNotBlockReceipt(t *testing.T) {
	// 630 is retained by ResumeRunChecked after an earlier initial-funding wait.
	// This later receipt needs only 240; a stage total cannot become its minimum.
	svc, db := round2PendingMoney(t, 240, 630)
	round2ManualPay(t, svc, 100)
	round2AssertOriginalSettles(t, svc, db, 80)
}

func TestRound2MoneyInactiveWaitsDoNotStarveFundedRecovery(t *testing.T) {
	svc, db := resumeFixture(t)
	mustExec(t, db, `DELETE FROM generation_runs WHERE id='r4';
		UPDATE users SET credits=1000 WHERE id='u1';
		UPDATE users SET status='banned' WHERE id='u2';
		UPDATE generation_runs SET waiting_reason='balance',required_credits=200,updated_at=now() WHERE id='r1'`)
	for i := 0; i < 10; i++ {
		project, runID := fmt.Sprintf("r2-block-p%d", i), fmt.Sprintf("r2-block-r%d", i)
		if _, err := db.Exec(`INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status)
			VALUES($1,'u2','synthetic',10000,5,'SCRIPT_REVIEW')`, project); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_runs(id,user_id,project_id,status,idempotency_key,input_hash,
			required_credits,waiting_reason,updated_at) VALUES($1,'u2',$2,'waiting_credits',$1,'synthetic',200,'balance',now()-interval '1 hour')`, runID, project); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		svc.AdvanceActiveRuns(context.Background(), 10)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id='r1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	jobs := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`)
	t.Logf("after three compensation scans: funded run=%s jobs=%d older inactive waits=10", status, jobs)
	if status == "waiting_credits" || jobs != 1 {
		t.Errorf("ineligible older waits permanently starve funded authorized run")
	}
}
