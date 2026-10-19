package service

// 充值订单的续跑上下文（run_id）契约测试。
// 覆盖：归属/状态校验、待支付订单复用不改写绑定、人工充值入账与唤醒同事务、
// 重复/并发回调只入账一次、坏绑定不得导致丢积分。

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func rechargeFixture(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := testutil.DB(t)
	svc := New(db, "synthetic-test-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES
		('u1','u1@example.com','x',0,'active'),
		('u2','u2@example.com','x',50000,'active'),
		('admin','admin@example.com','x',0,'active')`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status) VALUES
		('p1','u1','synthetic',100000,15,'SCRIPT_REVIEW'),
		('p2','u1','synthetic',100000,15,'SCRIPT_REVIEW'),
		('p3','u2','synthetic',100000,15,'SCRIPT_REVIEW')`)
	mustExec(t, db, `INSERT INTO generation_runs
		(id,user_id,project_id,status,idempotency_key,input_hash,credits_estimated) VALUES
		('r1','u1','p1','waiting_credits','k1','h1',630),
		('r2','u1','p2','waiting_credits','k2','h2',630),
		('r3','u2','p3','waiting_credits','k3','h3',630),
		('r4','u1','p1','cancelled','k4','h4',630)`)
	return svc, db
}

func resetPayRate() {
	rateMu.Lock()
	rateTable = map[string][]time.Time{}
	rateMu.Unlock()
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// 伪造/错误归属/不可续跑的 run 一律拒绝，且不得落任何订单。
func TestRechargeRejectsUntrustedRunContext(t *testing.T) {
	svc, db := rechargeFixture(t)
	cases := []struct {
		name  string
		runID string
	}{
		{"other owner", "r3"},
		{"cancelled", "r4"},
		{"missing", "does-not-exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetPayRate()
			if _, _, _, _, err := svc.CreatePayOrder("u1", 10000, "native", "", tc.runID); err == nil {
				t.Fatalf("CreatePayOrder 接受了 %s", tc.runID)
			}
			if _, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 100, RunID: tc.runID}); err == nil {
				t.Fatalf("CreateOrder 接受了 %s", tc.runID)
			}
		})
	}
	if n := countRows(t, db, `SELECT count(*) FROM recharge_orders`); n != 0 {
		t.Fatalf("拒绝的充值仍创建了 %d 张订单", n)
	}
}

// 本人等待中的执行可以绑定；复用待支付订单时绑定必须一致，不能串任务。
func TestRechargeBindsRunAndReuseNeverRewrites(t *testing.T) {
	t.Setenv("WXPAY_MOCK", "1")
	t.Setenv("NODE_ENV", "test")
	svc, db := rechargeFixture(t)

	resetPayRate()
	first, _, _, down, err := svc.CreatePayOrder("u1", 10000, "native", "", "r1")
	if err != nil || down {
		t.Fatalf("绑定 r1 失败 down=%v err=%v", down, err)
	}
	if first.RunID != "r1" {
		t.Fatalf("order.RunID = %q, want r1", first.RunID)
	}
	var stored string
	if err := db.QueryRow(`SELECT COALESCE(run_id,'') FROM recharge_orders WHERE id=$1`, first.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "r1" {
		t.Fatalf("DB run_id = %q, want r1", stored)
	}

	// 同一金额、不同 run：不得复用 r1 的待支付订单，更不能改写它的绑定。
	resetPayRate()
	second, _, _, _, err := svc.CreatePayOrder("u1", 10000, "native", "", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.RunID != "r2" {
		t.Fatalf("复用串任务: id=%s run=%s (first=%s)", second.ID, second.RunID, first.ID)
	}

	// 同 run 重复下单：复用原订单，绑定不变。
	resetPayRate()
	again, _, _, _, err := svc.CreatePayOrder("u1", 10000, "native", "", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.RunID != "r1" {
		t.Fatalf("未复用同上下文订单: got %s/%s want %s/r1", again.ID, again.RunID, first.ID)
	}

	// 普通充值（无上下文）不得复用带续跑绑定的订单。
	resetPayRate()
	plain, _, _, _, err := svc.CreatePayOrder("u1", 10000, "native", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plain.ID == first.ID || plain.RunID != "" {
		t.Fatalf("普通充值复用了续跑订单: id=%s run=%q", plain.ID, plain.RunID)
	}
}

// 人工充值：余额、流水、订单 paid/credited 与唤醒记录必须同一事务，且只做一次。
func TestManualOrderHandleCreditsAndEnqueuesWakeupOnce(t *testing.T) {
	svc, db := rechargeFixture(t)
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 100, RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if o.RunID != "r1" {
		t.Fatalf("order.RunID = %q, want r1", o.RunID)
	}
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	var credits int64
	if err := db.QueryRow(`SELECT credits FROM users WHERE id='u1'`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 10000 {
		t.Fatalf("credits = %d, want 10000", credits)
	}
	var paid bool
	var credited bool
	if err := db.QueryRow(`SELECT status='paid',credited FROM recharge_orders WHERE id=$1`, o.ID).Scan(&paid, &credited); err != nil {
		t.Fatal(err)
	}
	if !paid || !credited {
		t.Fatalf("paid=%v credited=%v, want both true", paid, credited)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='recharge'`); n != 1 {
		t.Fatalf("流水 %d 条, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM resume_wakeups WHERE run_id='r1' AND order_id=$1 AND user_id='u1' AND status='pending'`, o.ID); n != 1 {
		t.Fatalf("唤醒 %d 条, want 1", n)
	}

	// 重复审核不得二次入账或二次唤醒。
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true}); err == nil {
		t.Fatal("重复审核应被拒绝")
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='recharge'`); n != 1 {
		t.Fatalf("重复审核后流水 %d 条, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM resume_wakeups WHERE run_id='r1'`); n != 1 {
		t.Fatalf("重复审核后唤醒 %d 条, want 1", n)
	}
}

// 微信回调与主动查单并发：只能入账一次，只产生一条唤醒。
func TestSettleOrderConcurrentCallbacksCreditOnce(t *testing.T) {
	t.Setenv("WXPAY_MOCK", "1")
	t.Setenv("NODE_ENV", "test")
	svc, db := rechargeFixture(t)
	resetPayRate()
	o, _, _, _, err := svc.CreatePayOrder("u1", 10000, "native", "", "r1")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.settleOrder(o.ID, "", "", 10000, "txn-synthetic")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("settleOrder: %v", err)
		}
	}

	var credits int64
	if err := db.QueryRow(`SELECT credits FROM users WHERE id='u1'`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 10000 {
		t.Fatalf("credits = %d, want 10000", credits)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='recharge'`); n != 1 {
		t.Fatalf("流水 %d 条, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM resume_wakeups WHERE run_id='r1'`); n != 1 {
		t.Fatalf("唤醒 %d 条, want 1", n)
	}

	// 迟到重复通知仍然幂等。
	if err := svc.settleOrder(o.ID, "", "", 10000, "txn-synthetic"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='u1' AND kind='recharge'`); n != 1 {
		t.Fatalf("迟到通知后流水 %d 条, want 1", n)
	}
}

// 订单上的 run 关联失效（不存在或属于别人）时，钱必须照常入账，只是不唤醒。
func TestSettleOrderWithBrokenRunStillCredits(t *testing.T) {
	t.Setenv("WXPAY_MOCK", "1")
	t.Setenv("NODE_ENV", "test")
	for _, tc := range []struct{ name, runID string }{
		{"missing", "no-such-run"},
		{"foreign", "r3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := rechargeFixture(t)
			resetPayRate()
			o, _, _, _, err := svc.CreatePayOrder("u1", 10000, "native", "", "r1")
			if err != nil {
				t.Fatal(err)
			}
			// 直接破坏绑定，模拟历史脏数据：服务层写入路径不会产生这种订单。
			mustExec(t, db, `UPDATE recharge_orders SET run_id='`+tc.runID+`' WHERE id='`+o.ID+`'`)
			if err := svc.settleOrder(o.ID, "", "", 10000, "txn-broken"); err != nil {
				t.Fatalf("坏绑定导致入账失败: %v", err)
			}
			var credits int64
			if err := db.QueryRow(`SELECT credits FROM users WHERE id='u1'`).Scan(&credits); err != nil {
				t.Fatal(err)
			}
			if credits != 10000 {
				t.Fatalf("credits = %d, want 10000", credits)
			}
			if n := countRows(t, db, `SELECT count(*) FROM resume_wakeups`); n != 0 {
				t.Fatalf("坏绑定产生了 %d 条唤醒", n)
			}
		})
	}
}
