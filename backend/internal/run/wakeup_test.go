package run

import "testing"

func TestWakeupEnqueueIsIdempotent(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
	 VALUES('o1','u1',100,10000,'paid','native')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.EnqueueInTx(ctx, tx, r.ID, "o1", "u1"); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM resume_wakeups WHERE run_id=$1`, r.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("重复入账产生 %d 条唤醒，应为 1", n)
	}
}

func TestClaimIsExclusiveAndResumeFlipsRun(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWaitingCredits(ctx, r.ID, "balance", 300); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
	 VALUES('o1','u1',100,10000,'paid','native')`); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.BeginTx(ctx, nil)
	if err = s.EnqueueInTx(ctx, tx, r.ID, "o1", "u1"); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	first, err := s.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("首次抢占 %d 条，应为 1", len(first))
	}
	second, _ := s.ClaimDue(ctx, 10)
	if len(second) != 0 {
		t.Fatalf("已抢占的记录不应再次被抢，实际 %d", len(second))
	}

	okResumed, err := s.ResumeRun(ctx, first[0].RunID, first[0].UserID)
	if err != nil {
		t.Fatal(err)
	}
	if !okResumed {
		t.Fatal("等待态 run 应被放回 queued")
	}
	got, _ := s.ByID(ctx, r.ID)
	if got.Status != "queued" || got.WaitingReason != "" {
		t.Fatalf("续跑后状态 = %s/%q", got.Status, got.WaitingReason)
	}
	if err = s.MarkDone(ctx, first[0].ID, first[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
}

func TestResumeDoesNotStartCancelledRun(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	// 已取消：到账后不得启动
	if _, err = db.Exec(`UPDATE generation_runs SET status='cancelled' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	okResumed, err := s.ResumeRun(ctx, r.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if okResumed {
		t.Fatal("已取消的执行不得被充值唤醒启动")
	}
	got, _ := s.ByID(ctx, r.ID)
	if got.Status != "cancelled" {
		t.Fatalf("状态被改写为 %s", got.Status)
	}
}

func TestFailedWakeupRetriesThenDies(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
	 VALUES('o1','u1',100,10000,'paid','native')`); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.BeginTx(ctx, nil)
	s.EnqueueInTx(ctx, tx, r.ID, "o1", "u1")
	tx.Commit()
	ws, _ := s.ClaimDue(ctx, 10)
	if len(ws) != 1 {
		t.Fatal("应抢到 1 条")
	}
	// 连续失败，超过上限进入 dead；每次失败后重新认领（租约令牌递增）。
	if err = s.MarkFailed(ctx, ws[0].ID, ws[0].LeaseToken, "synthetic"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < wakeupMaxAttempts; i++ {
		if _, e := db.Exec(`UPDATE resume_wakeups SET run_after=now() WHERE id=$1`, ws[0].ID); e != nil {
			t.Fatal(e)
		}
		next, e := s.ClaimDue(ctx, 10)
		if e != nil || len(next) != 1 {
			t.Fatalf("reclaim %d: %v %d", i, e, len(next))
		}
		ws[0] = next[0]
		if err = s.MarkFailed(ctx, ws[0].ID, ws[0].LeaseToken, "synthetic"); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM resume_wakeups WHERE id=$1`, ws[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "dead" {
		t.Fatalf("超过上限应为 dead，实际 %s", status)
	}
}

// 别人的充值不能启动你的执行（任务书六.5）
func TestResumeRejectsWrongOwner(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWaitingCredits(ctx, r.ID, "balance", 300); err != nil {
		t.Fatal(err)
	}
	okResumed, err := s.ResumeRun(ctx, r.ID, "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	if okResumed {
		t.Fatal("非 owner 的续跑必须被拒绝")
	}
	got, _ := s.ByID(ctx, r.ID)
	if got.Status != "waiting_credits" {
		t.Fatalf("状态被非 owner 改写为 %s", got.Status)
	}
	_ = db
}

// R08：旧消费者不能覆盖新消费者对同一唤醒的处理结果。
func TestStaleWakeupConsumerCannotOverrideNewLease(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
	 VALUES('o1','u1',100,10000,'paid','native')`); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.BeginTx(ctx, nil)
	if err = s.EnqueueInTx(ctx, tx, r.ID, "o1", "u1"); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	first, err := s.ClaimDue(ctx, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: %v %d", err, len(first))
	}
	if n, e := s.ReclaimStale(ctx, 0); e != nil || n != 1 {
		t.Fatalf("reclaim: %v %d", e, n)
	}
	second, err := s.ClaimDue(ctx, 10)
	if err != nil || len(second) != 1 {
		t.Fatalf("second claim: %v %d", err, len(second))
	}
	if second[0].LeaseToken == first[0].LeaseToken {
		t.Fatal("lease token did not advance")
	}
	if err = s.MarkDone(ctx, first[0].ID, first[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM resume_wakeups WHERE id=$1`, first[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "done" {
		t.Fatal("stale consumer marked the reclaimed wakeup done")
	}
	if err = s.MarkDone(ctx, second[0].ID, second[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT status FROM resume_wakeups WHERE id=$1`, first[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("current consumer could not finish: %s", status)
	}
}
