package partner

import (
	"context"
	"database/sql"
	"testing"
)

func mkOrder(t *testing.T, s *Store, ctx context.Context, userID, id string) {
	t.Helper()
	if _, e := s.db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status,channel)
	 VALUES($1,$2,100,10000,'pending','native')`, id, userID); e != nil {
		t.Fatal(e)
	}
}

func withTx(t *testing.T, s *Store, ctx context.Context, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func TestSnapshotRecordsPartnerAndStatus(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, _ := s.Create(ctx, "admin", "u1", "甲", "", "")
	mkUser("u2")
	if err := bind(t, s, ctx, "u2", p.Code); err != nil {
		t.Fatal(err)
	}
	mkOrder(t, s, ctx, "u2", "o2")
	if err := withTx(t, s, ctx, func(tx *sql.Tx) error {
		return s.SnapshotOrder(ctx, tx, "o2", "u2")
	}); err != nil {
		t.Fatal(err)
	}
	pid, st, err := s.OrderSnapshot(ctx, "o2")
	if err != nil || pid != p.ID || st != StatusActive {
		t.Fatalf("快照 = %q/%q, %v; want %q/active", pid, st, err, p.ID)
	}
}

func TestSnapshotNotOverwrittenOnReuse(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, _ := s.Create(ctx, "admin", "u1", "甲", "", "")
	mkUser("u2")
	if err := bind(t, s, ctx, "u2", p.Code); err != nil {
		t.Fatal(err)
	}
	mkOrder(t, s, ctx, "u2", "o3")
	_ = withTx(t, s, ctx, func(tx *sql.Tx) error { return s.SnapshotOrder(ctx, tx, "o3", "u2") })

	// 代理随后被暂停：重开支付窗口 / 复用 pending 订单都不得改写原快照
	if _, err := s.SetStatus(ctx, "admin", p.ID, StatusSuspended, p.Version); err != nil {
		t.Fatal(err)
	}
	if err := withTx(t, s, ctx, func(tx *sql.Tx) error {
		return s.SnapshotOrder(ctx, tx, "o3", "u2")
	}); err != nil {
		t.Fatal(err)
	}
	pid, st, err := s.OrderSnapshot(ctx, "o3")
	if err != nil || pid != p.ID || st != StatusActive {
		t.Fatalf("复用后快照被改写 = %q/%q, %v; want %q/active", pid, st, err, p.ID)
	}
	var n int
	if err = s.db.QueryRow(`SELECT count(*) FROM partner_order_attributions WHERE order_id='o3'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("每笔订单最多一份快照，实际 %d", n)
	}
}

func TestSnapshotSuspendedPartnerStillRecords(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, _ := s.Create(ctx, "admin", "u1", "甲", "", "")
	mkUser("u2")
	if err := bind(t, s, ctx, "u2", p.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(ctx, "admin", p.ID, StatusSuspended, p.Version); err != nil {
		t.Fatal(err)
	}
	mkOrder(t, s, ctx, "u2", "o4")
	if err := withTx(t, s, ctx, func(tx *sql.Tx) error {
		return s.SnapshotOrder(ctx, tx, "o4", "u2")
	}); err != nil {
		t.Fatal(err)
	}
	pid, st, err := s.OrderSnapshot(ctx, "o4")
	if err != nil || pid != p.ID || st != StatusSuspended {
		t.Fatalf("老客户暂停后仍应记录来源与下单状态 = %q/%q, %v", pid, st, err)
	}
}

func TestSnapshotSkipsUnattributedUser(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	mkOrder(t, s, ctx, "u1", "o5")
	if err := withTx(t, s, ctx, func(tx *sql.Tx) error {
		return s.SnapshotOrder(ctx, tx, "o5", "u1")
	}); err != nil {
		t.Fatalf("无归属用户下单不应报错: %v", err)
	}
	pid, _, err := s.OrderSnapshot(ctx, "o5")
	if err != nil || pid != "" {
		t.Fatalf("无归属不应产生快照，实际 %q %v", pid, err)
	}
}
