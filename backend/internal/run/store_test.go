package run

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"drama-platform/internal/testutil"
)

func fixture(t *testing.T) (*Store, context.Context, *sql.DB) {
	t.Helper()
	db := testutil.DB(t)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,credits) VALUES('u1','u1@example.com','x',1000)`,
		`INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('p1','u1','synthetic',1000,'DRAFT')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return New(db), context.Background(), db
}

func TestCreateRunStartsAtVersionOne(t *testing.T) {
	s, ctx, _ := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ProjectVersion != 1 || r.Status != "queued" || r.Mode != "first" || !r.AutoResume {
		t.Fatalf("run = v%d %s %s auto=%v", r.ProjectVersion, r.Status, r.Mode, r.AutoResume)
	}
}

// 重复点击 / 双标签页：同键同输入必须返回同一个 run，不能创建第二个付费执行
func TestCreateRunIsIdempotent(t *testing.T) {
	s, ctx, _ := fixture(t)
	a, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("同键同输入应返回同一 run：%s vs %s", a.ID, b.ID)
	}
}

// 同键不同输入必须拒绝，不能覆盖旧输入
func TestCreateRunRejectsConflictingInput(t *testing.T) {
	s, ctx, _ := fixture(t)
	if _, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h2"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
}

// 同一项目同时只允许一个进行中的执行（服务端互斥）
func TestCreateRunRejectsConcurrentRun(t *testing.T) {
	s, ctx, _ := fixture(t)
	if _, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k2", InputHash: "h2"}); !errors.Is(err, ErrRunActive) {
		t.Fatalf("want ErrRunActive, got %v", err)
	}
}

// 修改生成建立新版本，不改写旧版本
func TestCreateRunRevisionBumpsVersion(t *testing.T) {
	s, ctx, db := fixture(t)
	first, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE generation_runs SET status='completed' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	rev, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", Mode: "revise", IdempotencyKey: "k2", InputHash: "h2"})
	if err != nil {
		t.Fatal(err)
	}
	if rev.ProjectVersion != 2 || rev.Mode != "revise" {
		t.Fatalf("revise = v%d %s", rev.ProjectVersion, rev.Mode)
	}
	old, _ := s.ByID(ctx, first.ID)
	if old.ProjectVersion != 1 || old.Status != "completed" {
		t.Fatalf("旧版本被改写: v%d %s", old.ProjectVersion, old.Status)
	}
}

// 积分不足也要留下可恢复的执行记录
func TestWaitingCreditsKeepsRun(t *testing.T) {
	s, ctx, _ := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWaitingCredits(ctx, r.ID, "balance", 300); err != nil {
		t.Fatal(err)
	}
	got, err := s.ByID(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "waiting_credits" || got.WaitingReason != "balance" || got.RequiredCredits < 300 {
		t.Fatalf("等待状态 = %s/%s/%d", got.Status, got.WaitingReason, got.RequiredCredits)
	}
}

// 复审 B2：已结束的执行不得被 MarkWaitingCredits 复活。
func TestMarkWaitingRejectsFinishedRun(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{"cancelled", "completed", "failed"} {
		if _, err = db.Exec(`UPDATE generation_runs SET status=$2 WHERE id=$1`, r.ID, st); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkWaitingCredits(ctx, r.ID, "balance", 500); !errors.Is(err, ErrRunNotActive) {
			t.Fatalf("%s 状态下应拒绝进入等待态，实际 %v", st, err)
		}
		got, _ := s.ByID(ctx, r.ID)
		if got.Status != st {
			t.Fatalf("状态被改写：%s → %s", st, got.Status)
		}
	}
}

// 复审 M2：估算为 0 时不得放行（fail closed）。
func TestResumeFailsClosedOnZeroEstimate(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE generation_runs SET status='waiting_credits', credits_estimated=0 WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeRun(ctx, r.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("估算为 0 时必须 fail closed，不得启动")
	}
}
