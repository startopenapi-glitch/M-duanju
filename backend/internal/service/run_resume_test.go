package service

// 充值到账 → 持久化唤醒 → worker 消费 → 续跑到后续 job 的闭环测试。
// 使用隔离测试库与模拟支付，不触达真实渠道或生产数据。

import (
	"context"
	"database/sql"
	"testing"

	"drama-platform/internal/model"
	runpkg "drama-platform/internal/run"
)

func resumeFixture(t *testing.T) (*Service, *sql.DB) {
	svc, db := rechargeFixture(t)
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_REVIEW',keyvisual_url='http://127.0.0.1/kv.jpg' WHERE id='p1'`)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES('s1','p1','S01',1,5)`)
	return svc, db
}

func payAndSettle(t *testing.T, svc *Service, yuan float64, runID string) *model.RechargeOrder {
	t.Helper()
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: yuan, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	return o
}

// 到账后应真正续跑到下一个付费 job，而不是只把 run 改成 queued。
func TestResumeWakeupContinuesToNextJob(t *testing.T) {
	svc, db := resumeFixture(t)
	payAndSettle(t, svc, 100, "r1")

	n, err := svc.ResumeFromWakeups(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("处理唤醒 %d 条, want 1", n)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id='r1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("run status = %s, want running", status)
	}
	var kind, jstatus, jobRun string
	var reserved int64
	if err := db.QueryRow(`SELECT kind,status,COALESCE(run_id,''),reserved_credits FROM jobs WHERE project_id='p1'`).Scan(&kind, &jstatus, &jobRun, &reserved); err != nil {
		t.Fatal(err)
	}
	if kind != "shot" || jstatus != "queued" || reserved <= 0 {
		t.Fatalf("后续 job = %s/%s/%d", kind, jstatus, reserved)
	}
	if jobRun != "r1" {
		t.Fatalf("job.run_id = %q, want r1", jobRun)
	}
	if n := countRows(t, db, `SELECT count(*) FROM shots WHERE id='s1' AND status='QUEUED'`); n != 1 {
		t.Fatalf("分镜未进入排队态")
	}
	if n := countRows(t, db, `SELECT count(*) FROM resume_wakeups WHERE status='done'`); n != 1 {
		t.Fatalf("唤醒未标记 done")
	}

	// 重复推进不得产生第二个付费 job。
	if err := svc.AdvanceRun(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 1 {
		t.Fatalf("重复推进产生了 %d 个 job", n)
	}
	// 幂等：再次消费不会重复处理。
	if n, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("第二次消费 n=%d err=%v", n, err)
	}
}

// 充值仍不足：入账照常，run 继续等待且不派发 job；再次充值后恢复。
func TestResumeWaitsWhenStillShortAndResumesOnNextTopUp(t *testing.T) {
	svc, db := resumeFixture(t)
	// r1 估算 630；¥1=100 积分，不足。
	payAndSettle(t, svc, 1, "r1")
	if n, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("first n=%d err=%v", n, err)
	}
	var status, reason string
	if err := db.QueryRow(`SELECT status,waiting_reason FROM generation_runs WHERE id='r1'`).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "waiting_credits" || reason != "balance" {
		t.Fatalf("不足时应继续等待，实际 %s/%s", status, reason)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 0 {
		t.Fatalf("不足时派发了 %d 个 job", n)
	}

	payAndSettle(t, svc, 10, "r1")
	if n, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("second n=%d err=%v", n, err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 1 {
		t.Fatalf("补足后应派发 1 个 job，实际 %d", n)
	}
}

// 已取消：钱照常到账，但不启动失效执行。
func TestResumeDoesNotStartCancelledRunButKeepsCredit(t *testing.T) {
	svc, db := resumeFixture(t)
	// 先下单（绑定有效），支付前执行被取消：钱仍须入账，但不能启动。
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 100, RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE generation_runs SET status='cancelled' WHERE id='r1'`)
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 0 {
		t.Fatalf("取消后仍派发 %d 个 job", n)
	}
	var credits int64
	if err := db.QueryRow(`SELECT credits FROM users WHERE id='u1'`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 10000 {
		t.Fatalf("取消后未入账: credits=%d", credits)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id='r1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("取消状态被改写为 %s", status)
	}
}

// 被新版本替代的旧执行：入账但不启动。
func TestResumeDoesNotStartSupersededRun(t *testing.T) {
	svc, db := resumeFixture(t)
	mustExec(t, db, `INSERT INTO generation_runs(id,user_id,project_id,project_version,status,idempotency_key,input_hash,credits_estimated)
		VALUES('r9','u1','p1',2,'completed','k9','h9',630)`)
	payAndSettle(t, svc, 100, "r1")
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 0 {
		t.Fatalf("旧版本仍派发 %d 个 job", n)
	}
}

// 账号停用：不启动新生成，但已付款照常入账。
func TestResumeDoesNotStartForInactiveAccount(t *testing.T) {
	svc, db := resumeFixture(t)
	mustExec(t, db, `UPDATE users SET status='banned' WHERE id='u1'`)
	payAndSettle(t, svc, 100, "r1")
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 0 {
		t.Fatalf("停用账号仍派发 %d 个 job", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1'`); n != 1 {
		t.Fatalf("停用账号入账流水 %d 条", n)
	}
}

// worker 崩溃后 claimed 租约可被回收并重新消费。
func TestWakeupLeaseReclaimsAfterCrash(t *testing.T) {
	_, db := rechargeFixture(t)
	store := runpkg.New(db)
	mustExec(t, db, `INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
		VALUES('o1','u1',100,10000,'paid','native')`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueInTx(context.Background(), tx, "r1", "o1", "u1"); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	first, err := store.ClaimDue(context.Background(), 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("claim: %v %d", err, len(first))
	}
	if n, err := store.ReclaimStale(context.Background(), 0); err != nil || n != 1 {
		t.Fatalf("reclaim: %v n=%d", err, n)
	}
	again, err := store.ClaimDue(context.Background(), 10)
	if err != nil || len(again) != 1 {
		t.Fatalf("reclaim 后应可再次认领: %v %d", err, len(again))
	}
}

// 入账提交后进程崩溃：只留下持久化唤醒，重启补扫仍能恢复。
func TestBackfillFindsCreditedOrderWithoutWakeup(t *testing.T) {
	_, db := rechargeFixture(t)
	store := runpkg.New(db)
	mustExec(t, db, `INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel,credited,run_id)
		VALUES('o1','u1',100,10000,'paid','native',true,'r1')`)
	if n, err := store.BackfillMissing(context.Background()); err != nil || n != 1 {
		t.Fatalf("backfill: %v n=%d", err, n)
	}
	if n, err := store.BackfillMissing(context.Background()); err != nil || n != 0 {
		t.Fatalf("backfill 非幂等: %v n=%d", err, n)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM resume_wakeups WHERE run_id='r1' AND order_id='o1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("wakeup status=%s", status)
	}
	// 清理后确认 ClaimDue 能看到它。
	ws, err := store.ClaimDue(context.Background(), 10)
	if err != nil || len(ws) != 1 {
		t.Fatalf("claim backfilled: %v %d", err, len(ws))
	}
}

// 取消后停止派发新任务；已排队任务不被删除，但不会再产生第二个付费 job。
func TestCancelRunStopsFurtherDispatch(t *testing.T) {
	svc, db := resumeFixture(t)
	payAndSettle(t, svc, 100, "r1")
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 1 {
		t.Fatalf("resume 后 job=%d, want 1", n)
	}
	if err := svc.CancelRun("u1", "p1", "r1"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id='r1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("status=%s, want cancelled", status)
	}
	if err := svc.AdvanceRun(context.Background(), "r1"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 1 {
		t.Fatalf("取消后仍派发了新 job: %d", n)
	}
	if err := svc.CancelRun("u1", "p1", "r1"); err != nil {
		t.Fatalf("同一取消请求重放应幂等成功: %v", err)
	}
	if err := svc.CancelRun("u1", "p2", "r1"); err == nil {
		t.Fatal("跨项目取消应被拒绝")
	}
}
