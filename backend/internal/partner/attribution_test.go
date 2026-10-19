package partner

import (
	"context"
	"errors"
	"testing"
)

func bind(t *testing.T, s *Store, ctx context.Context, userID, code string) error {
	t.Helper()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	e := s.BindOnRegister(ctx, tx, userID, code)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func TestBindActivePartner(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	mkUser("u2")
	if err = bind(t, s, ctx, "u2", p.Code); err != nil {
		t.Fatal(err)
	}
	pid, err := s.AttributedPartnerID(ctx, "u2")
	if err != nil || pid != p.ID {
		t.Fatalf("归属 = %q, %v; want %q", pid, err, p.ID)
	}
}

func TestBindFirstSourceWins(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	mkUser("u2b")
	a, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "admin", "u2b", "乙", "", "")
	if err != nil {
		t.Fatal(err)
	}
	mkUser("u3")
	if err = bind(t, s, ctx, "u3", a.Code); err != nil {
		t.Fatal(err)
	}
	// 随后访问另一个代理链接：不得覆盖首次归属
	if err = bind(t, s, ctx, "u3", b.Code); err != nil {
		t.Fatal(err)
	}
	pid, err := s.AttributedPartnerID(ctx, "u3")
	if err != nil || pid != a.ID {
		t.Fatalf("A/B 竞争后归属 = %q, %v; want %q", pid, err, a.ID)
	}
}

func TestBindSuspendedPartnerDoesNotBind(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetStatus(ctx, "admin", p.ID, StatusSuspended, p.Version); err != nil {
		t.Fatal(err)
	}
	mkUser("u2")
	if err = bind(t, s, ctx, "u2", p.Code); !errors.Is(err, ErrSuspended) {
		t.Fatalf("want ErrSuspended, got %v", err)
	}
	pid, _ := s.AttributedPartnerID(ctx, "u2")
	if pid != "" {
		t.Fatalf("暂停代理不应产生归属，实际 %q", pid)
	}
}

func TestBindUnknownOrEmptyCodeDoesNotBlockRegister(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	if err := bind(t, s, ctx, "u1", "nonexistent-code"); err != nil {
		t.Fatalf("无效来源不应阻断注册: %v", err)
	}
	if err := bind(t, s, ctx, "u1", ""); err != nil {
		t.Fatalf("空来源不应阻断注册: %v", err)
	}
	pid, _ := s.AttributedPartnerID(ctx, "u1")
	if pid != "" {
		t.Fatalf("无效来源不应产生归属，实际 %q", pid)
	}
}

// 数据库异常必须向上抛，不能被吞掉伪装成绑定成功。
// 外键在 INSERT 时即校验，所以这里直接断言返回错误，并确认回滚后无残留。
func TestBindDbErrorIsNotSwallowed(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("u1")
	p, err := s.Create(ctx, "admin", "u1", "甲", "", "")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = s.BindOnRegister(ctx, tx, "u-missing", p.Code); err == nil {
		t.Fatal("用户不存在时绑定必须返回错误，不得伪装成功")
	}
	_ = tx.Rollback()
	var n int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM partner_attributions WHERE user_id='u-missing'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("回滚后不应残留归属，实际 %d 条", n)
	}
}
