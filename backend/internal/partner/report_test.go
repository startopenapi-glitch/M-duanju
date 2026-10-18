package partner

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReportCountsIsolationAndMasking(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	// 两个代理
	mkUser("ownerA")
	mkUser("ownerB")
	pa, err := s.Create(ctx, "admin", "ownerA", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := s.Create(ctx, "admin", "ownerB", "乙", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// A 的两个客户：u1 有项目+已付订单，u2 什么都没有
	mkUser("u1")
	mkUser("u2")
	if err = bind(t, s, ctx, "u1", pa.Code); err != nil {
		t.Fatal(err)
	}
	if err = bind(t, s, ctx, "u2", pa.Code); err != nil {
		t.Fatal(err)
	}
	// B 的一个客户：必须与 A 完全隔离
	mkUser("u3")
	if err = bind(t, s, ctx, "u3", pb.Code); err != nil {
		t.Fatal(err)
	}

	if _, err = s.db.Exec(`INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('pr1','u1','synthetic',1000,'SCRIPT_REVIEW')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel,mock)
	 VALUES('ordA','u1',100,10000,'paid','native',false)`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel,mock)
	 VALUES('ordB','u3',100,10000,'paid','native',false)`); err != nil {
		t.Fatal(err)
	}
	// 订单快照决定归属
	if err = withTx(t, s, ctx, func(tx *sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}

	ov, err := s.Overview(ctx, pa.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ov.NewUsers != 2 || ov.ProjectUsers != 1 || ov.PayingUsers != 1 {
		t.Fatalf("概览 = %d/%d/%d, want 2/1/1", ov.NewUsers, ov.ProjectUsers, ov.PayingUsers)
	}
	if ov.CommissionOn {
		t.Fatal("一期分佣必须为关闭")
	}

	cs, err := s.Customers(ctx, pa.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("客户数 = %d, want 2", len(cs))
	}
	for _, c := range cs {
		if !strings.HasPrefix(c.Alias, "客户 ") {
			t.Fatalf("别名未脱敏: %q", c.Alias)
		}
		if strings.Contains(c.Alias, "@") || strings.Contains(strings.ToLower(c.Alias), "u1") {
			t.Fatalf("别名泄露原始标识: %q", c.Alias)
		}
	}
	// 同一客户在一次请求与再次请求之间别名必须稳定
	cs2, _ := s.Customers(ctx, pa.ID)
	if len(cs2) == len(cs) && cs2[0].Alias != cs[0].Alias {
		t.Fatal("别名必须稳定")
	}

	// B 看不到 A 的客户
	cb, _ := s.Customers(ctx, pb.ID)
	if len(cb) != 1 {
		t.Fatalf("B 的客户数 = %d, want 1（数据隔离）", len(cb))
	}
}

func TestOrdersOnlyFromSnapshotAndMasked(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("owner")
	p, err := s.Create(ctx, "admin", "owner", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	mkUser("u1")
	if err = bind(t, s, ctx, "u1", p.Code); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
	 VALUES('order-abcdef123456','u1',500,50000,'paid','native')`); err != nil {
		t.Fatal(err)
	}
	// 有订单但没有快照 -> 不计入（快照是权威来源）
	rows, err := s.Orders(ctx, p.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("无快照订单不应计入，实际 %d", len(rows))
	}
	// 写入快照后计入，且编号脱敏
	if err = withTx(t, s, ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO partner_order_attributions(id,order_id,partner_id,partner_status_at_pay)
		 VALUES('pa1','order-abcdef123456',$1,'active')`, p.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Orders(ctx, p.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("订单数 = %d, want 1", len(rows))
	}
	if strings.Contains(rows[0].MaskedID, "abcdef") || !strings.HasPrefix(rows[0].MaskedID, "****") {
		t.Fatalf("订单编号未脱敏: %q", rows[0].MaskedID)
	}
	if rows[0].AmountYuan != "500" {
		t.Fatalf("金额 = %q, want 500", rows[0].AmountYuan)
	}
}
