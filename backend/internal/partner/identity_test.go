package partner

import (
	"context"
	"errors"
	"testing"

	"drama-platform/internal/testutil"
)

func fixture(t *testing.T) (*Store, context.Context, func(string)) {
	t.Helper()
	db := testutil.DB(t)
	s := New(db)
	ctx := context.Background()
	mkUser := func(id string) {
		if _, e := db.Exec(`INSERT INTO users(id,email,password_hash,credits) VALUES($1,$2,'unused',0)`,
			id, id+"@example.com"); e != nil {
			t.Fatal(e)
		}
	}
	return s, ctx, mkUser
}

func TestPartnerOpenGeneratesUniqueCode(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	mkUser("u2")
	a, err := s.Create(ctx, "admin", "u1", "合作一号", "简介", "wx1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "admin", "u2", "合作二号", "", "wx2")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Code) < 12 || len(b.Code) < 12 {
		t.Fatalf("推广码长度不足 12: %q %q", a.Code, b.Code)
	}
	if a.Code == b.Code {
		t.Fatal("推广码必须唯一")
	}
	if a.Status != StatusActive {
		t.Fatalf("新开通状态应为 active，实际 %s", a.Status)
	}
}

func TestPartnerOpenDuplicateUserRejected(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	if _, err := s.Create(ctx, "admin", "u1", "甲", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "admin", "u1", "乙", "", ""); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
}

func TestPartnerStatusVersionConflict(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	suspended, err := s.SetStatus(ctx, "admin", p.ID, StatusSuspended, p.Version)
	if err != nil {
		t.Fatal(err)
	}
	if suspended.Status != StatusSuspended || suspended.Version != p.Version+1 {
		t.Fatalf("状态或版本未推进: %s v%d", suspended.Status, suspended.Version)
	}
	// 用过期版本再改一次：必须冲突，不能覆盖
	if _, err := s.SetStatus(ctx, "admin", p.ID, StatusActive, p.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("want ErrVersionConflict, got %v", err)
	}
}

func TestPartnerInvalidStatusRejected(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"deleted", "pending", "", "ACTIVE"} {
		if _, err := s.SetStatus(ctx, "admin", p.ID, bad, p.Version); err == nil {
			t.Fatalf("非法状态 %q 被接受", bad)
		}
	}
}

func TestPartnerAuditAppended(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetStatus(ctx, "admin", p.ID, StatusSuspended, p.Version); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM partner_audit_events WHERE partner_id=$1`, p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("开通与暂停都应留审计，实际 %d 条", n)
	}
}

func TestPublicProfileSuspendedNotUsable(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "简介", "wx")
	if err != nil {
		t.Fatal(err)
	}
	pp, err := s.PublicProfile(ctx, p.Code)
	if err != nil || !pp.Usable {
		t.Fatalf("active 应可用: %v %v", pp, err)
	}
	if _, err = s.SetStatus(ctx, "admin", p.ID, StatusSuspended, p.Version); err != nil {
		t.Fatal(err)
	}
	pp, err = s.PublicProfile(ctx, p.Code)
	if err != nil {
		t.Fatal(err)
	}
	if pp.Usable || pp.Reason == "" {
		t.Fatal("暂停后应不可用并给出原因")
	}
	if _, err = s.PublicProfile(ctx, "nonexistent-code"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
