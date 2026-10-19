package service

// 本轮 8 项整改的补充回归：取消/提交边界、run-job 原子绑定、待结算恢复、
// 历史恢复矩阵。全部使用隔离库与本地 mock。

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func providerSubmitCounter(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			creates.Add(1)
			fmt.Fprint(w, `{"id":"remediation-task"}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	return srv, &creates
}

// R01 反向时序：job 已被认领但尚未提交时取消，提交边界必须拒绝付费请求。
func TestCancelAfterClaimBlocksProviderSubmission(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	_, creates := providerSubmitCounter(t)
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	job := claimOne(t, svc, JobKeyVisual)
	var rs string
	db.QueryRow(`SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&rs)
	t.Logf("before cancel run=%s jobRun=%q phase=%s", rs, job.RunID, job.Phase)
	if err := svc.CancelRun("review-u", "review-p", runID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ExecuteJob(job); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&rs)
	t.Logf("after execute run=%s jobRun=%q creates=%d", rs, job.RunID, creates.Load())
	if n := creates.Load(); n != 0 {
		t.Fatalf("cancel after claim still submitted %d provider job(s)", n)
	}
	var status string
	var reserved int64
	if err := db.QueryRow(`SELECT status,reserved_credits FROM jobs WHERE id=$1`, job.ID).Scan(&status, &reserved); err != nil {
		t.Fatal(err)
	}
	if status != JobCancelled || reserved != 0 {
		t.Fatalf("job after cancel = %s reserved=%d", status, reserved)
	}
	// 重复取消不得再次释放或改写。
	before := countRows(t, db, `SELECT count(*) FROM jobs WHERE status='cancelled'`)
	if err := svc.CancelRun("review-u", "review-p", runID); err != nil {
		t.Fatalf("same cancellation replay should succeed without mutation: %v", err)
	}
	if after := countRows(t, db, `SELECT count(*) FROM jobs WHERE status='cancelled'`); after != before {
		t.Fatalf("duplicate cancel mutated jobs: %d -> %d", before, after)
	}
}

// R03 写入点失败：插入 job 失败不得留下半成品；故障移除后可重试成功且只创建一组记录。
func TestRunBoundEnqueueFailureRollsBackAndRetries(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	mustExec(t, db, `CREATE FUNCTION remediation_reject_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic insert failure'; END $$`)
	mustExec(t, db, `CREATE TRIGGER remediation_job_insert BEFORE INSERT ON jobs
		FOR EACH ROW EXECUTE PROCEDURE remediation_reject_insert()`)
	if err := svc.AdvanceRun(context.Background(), runID); err == nil {
		t.Fatal("expected enqueue failure to surface as retryable error")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='review-p'`); n != 0 {
		t.Fatalf("failed enqueue left %d job(s)", n)
	}
	mustExec(t, db, `DROP TRIGGER remediation_job_insert ON jobs`)
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1`, runID); n != 1 {
		t.Fatalf("retry created %d run-bound jobs, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id IS NULL`); n != 0 {
		t.Fatalf("retry left %d unowned job(s)", n)
	}
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='review-p'`); n != 1 {
		t.Fatalf("duplicate advance created extra job(s): %d", n)
	}
}

// R03 归属/版本/旧入口：错误 run、已取消 run、旧手动入口都不能派发新 job。
func TestRunBoundEnqueueRejectsWrongAuthorization(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	if _, err := svc.EnqueueJobForRun("not-a-run", JobKeyVisual, "review-p"); err == nil {
		t.Fatal("unknown run accepted")
	}
	if _, err := svc.EnqueueJob(JobKeyVisual, "review-p", "", 0, ""); !errors.Is(err, ErrRunManaged) {
		t.Fatalf("legacy entry on auto project = %v, want ErrRunManaged", err)
	}
	if err := svc.CancelRun("review-u", "review-p", runID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnqueueJobForRun(runID, JobKeyVisual, "review-p"); err == nil {
		t.Fatal("cancelled run accepted new job")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='review-p'`); n != 0 {
		t.Fatalf("rejected enqueue left %d job(s)", n)
	}
}

// R02 完整恢复：补款后只结算一次、只扣一次，且不新增生成请求。
func TestPendingSettlementResumesOnceWithoutRegeneration(t *testing.T) {
	svc, db := rechargeFixture(t)
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(testEncoder(t)), "ffprobe"))
	mustExec(t, db, `UPDATE users SET credits=220 WHERE id='u1';
		UPDATE projects SET status='SHOTS_GENERATING' WHERE id='p1';
		UPDATE generation_runs SET status='running' WHERE id='r1';
		INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id,duration)
		VALUES('remediation-s1','p1','S01',1,'QUEUED','remediation-video',5);
		INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,reserved_credits,run_id)
		VALUES('remediation-job','shot','p1','remediation-s1',1,'queued','polling','remediation-video',200,'r1')`)
	j := claimOne(t, svc, JobShot)
	payload := syntheticVideo(t)
	digest := sha256.Sum256(payload)
	file := filepath.Join(svc.storageDir("videos"), fmt.Sprintf("%s-%x.mp4", generationArtifactKey(j.UpstreamID), digest))
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	in := GenerationSettlement{Kind: "video", TaskID: j.UpstreamID, ProjectID: "p1", ShotID: "remediation-s1",
		URL: svc.publicURL(file), ChargeMicros: 2400000, Duration: 5, Width: 1344, Height: 768}
	if _, err := svc.withJob(j).SettleGeneration(in); err == nil {
		t.Fatal("expected pending receipt")
	}
	if err := svc.ExecuteJob(j); err == nil {
		t.Fatal("expected parked settlement")
	}
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 10, RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "remediation"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
		svc.AdvanceActiveRuns(context.Background(), 10)
	}
	job := claimOne(t, svc, JobShot)
	if err := svc.ExecuteJob(job); err != nil {
		t.Fatal(err)
	}
	var receipt, jobStatus string
	var balance int64
	if err := db.QueryRow(`SELECT g.status,j.status,u.credits FROM generation_settlements g
		JOIN jobs j ON j.upstream_id=g.upstream_id JOIN users u ON u.id=g.user_id
		WHERE g.upstream_id='remediation-video'`).Scan(&receipt, &jobStatus, &balance); err != nil {
		t.Fatal(err)
	}
	if receipt != "settled" || jobStatus != "done" {
		t.Fatalf("receipt/job = %s/%s, want settled/done", receipt, jobStatus)
	}
	if balance != 980 {
		t.Fatalf("balance = %d, want 980 (220+1000-240)", balance)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='spend'`); n != 1 {
		t.Fatalf("spend transactions = %d, want 1", n)
	}
}

// R06 图片阶段失败：旧失败记录不阻止新授权重新出图，且新 job 归属新 run。
func TestRecoveryRetriesFailedKeyVisualUnderNewRun(t *testing.T) {
	db := testutilDB(t)
	svc := New(db, "remediation-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,nickname,credits,status)
		VALUES('remediation-u','remediation@example.com','x','R',10000,'active')`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status)
		VALUES('remediation-p','remediation-u','synthetic',10000,5,'FAILED')`)
	mustExec(t, db, `INSERT INTO characters(id,project_id,code,name) VALUES('remediation-c','remediation-p','S1','角色')`)
	mustExec(t, db, `INSERT INTO jobs(kind,project_id,status,phase,attempts,max_attempts,error_code)
		VALUES('keyvisual','remediation-p','failed','provider_failed',2,2,'content_failed')`)
	start, err := svc.StartRun("remediation-u", "remediation-p", &StartRunReq{IdempotencyKey: "remediation-image"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRow(`SELECT COALESCE(run_id,'') FROM jobs
		WHERE project_id='remediation-p' AND kind='keyvisual' AND status IN ('queued','running')`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if runID != start.Run.ID {
		t.Fatalf("new keyvisual job run_id = %q, want %s", runID, start.Run.ID)
	}
}

// R06 合成阶段：完整素材复用，只为新 run 创建一个合成 job。
func TestRecoveryReusesDeliveredShotsForAssemble(t *testing.T) {
	db := testutilDB(t)
	svc := New(db, "remediation-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,nickname,credits,status)
		VALUES('remediation-u','remediation@example.com','x','R',10000,'active')`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status,keyvisual_url)
		VALUES('remediation-p','remediation-u','synthetic',10000,5,'SHOTS_REVIEW','synthetic.jpg')`)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,duration,status,video_url,upstream_id)
		VALUES('remediation-s','remediation-p','S01',1,5,'APPROVED','synthetic.mp4','old-video')`)
	mustExec(t, db, `INSERT INTO jobs(kind,project_id,status,phase,attempts,max_attempts,error_code)
		VALUES('assemble','remediation-p','failed','provider_failed',2,2,'content_failed')`)
	start, err := svc.StartRun("remediation-u", "remediation-p", &StartRunReq{IdempotencyKey: "remediation-assemble"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND kind='shot'`, start.Run.ID); n != 0 {
		t.Fatalf("assemble recovery regenerated %d shot job(s)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1 AND kind='assemble' AND status IN ('queued','running')`, start.Run.ID); n != 1 {
		t.Fatalf("assemble recovery created %d assemble job(s), want 1", n)
	}
}

// R06 同一 run 的合成失败不无限重试；失败镜头达到上限也不再派发。
func TestRecoveryStopsAtRetryLimits(t *testing.T) {
	db := testutilDB(t)
	svc := New(db, "remediation-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,nickname,credits,status)
		VALUES('remediation-u','remediation@example.com','x','R',10000,'active')`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status,keyvisual_url)
		VALUES('remediation-p','remediation-u','synthetic',10000,5,'SHOTS_REVIEW','synthetic.jpg')`)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,duration,status,retry_count)
		VALUES('remediation-s','remediation-p','S01',1,5,'FAILED',2)`)
	start, err := svc.StartRun("remediation-u", "remediation-p", &StartRunReq{IdempotencyKey: "remediation-limit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	if err := db.QueryRow(`SELECT status,waiting_reason FROM generation_runs WHERE id=$1`, start.Run.ID).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || reason != "shot_failed" {
		t.Fatalf("run = %s/%s, want failed/shot_failed", state, reason)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1`, start.Run.ID); n != 0 {
		t.Fatalf("retry limit still created %d job(s)", n)
	}
}

func testutilDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.DB(t)
	return db
}
