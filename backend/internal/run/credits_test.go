package run

import (
	"context"
	"testing"
)

func TestAvailableCreditsSubtractsActiveReservations(t *testing.T) {
	s, ctx, db := fixture(t)
	// 余额 1000，无预占
	avail, err := s.AvailableCredits(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if avail != 1000 {
		t.Fatalf("可用 = %d, want 1000", avail)
	}
	// 一个进行中的任务预占 300
	if _, err = db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,reserved_credits,lease_token,phase)
	 VALUES('j1','shot','p1','',1,'running',300,0,'polling')`); err != nil {
		t.Fatal(err)
	}
	avail, err = s.AvailableCredits(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if avail != 700 {
		t.Fatalf("可用 = %d, want 700（余额扣除有效预占）", avail)
	}
	// 已结算的任务不再占用
	if _, err = db.Exec(`UPDATE jobs SET status='done', reserved_credits=0 WHERE id='j1'`); err != nil {
		t.Fatal(err)
	}
	avail, _ = s.AvailableCredits(ctx, "u1")
	if avail != 1000 {
		t.Fatalf("结算后可用 = %d, want 1000", avail)
	}
}

func TestShortfallReportsGap(t *testing.T) {
	s, ctx, db := fixture(t)
	if _, err := db.Exec(`UPDATE users SET credits=200 WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}
	gap, err := s.Shortfall(ctx, "u1", 500)
	if err != nil {
		t.Fatal(err)
	}
	if gap != 300 {
		t.Fatalf("缺口 = %d, want 300", gap)
	}
	gap, _ = s.Shortfall(ctx, "u1", 100)
	if gap != 0 {
		t.Fatalf("足够时缺口应为 0，实际 %d", gap)
	}
}

var _ = context.Background
