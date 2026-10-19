// 红测试：定义「续跑前必须重新校验」的缺口（任务书六.5）。
// 余额校验已实现，本文件已转为常规回归。
package run

import (
	"context"
	"testing"
)

// 余额仍不足时，充值到账也不得启动生成，只更新缺口。
func TestResumeRejectsInsufficientCredits(t *testing.T) {
	s, ctx, db := fixture(t)
	r, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET credits=0 WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWaitingCredits(ctx, r.ID, "balance", 500); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeRun(ctx, r.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("余额为 0 时不得把 run 放回 queued（当前实现没有余额校验）")
	}
	got, _ := s.ByID(ctx, r.ID)
	if got.Status != "waiting_credits" {
		t.Fatalf("状态被改写为 %s", got.Status)
	}
}

// 项目已被新版本替代时，旧执行不得被充值唤醒。
func TestResumeRejectsSupersededRun(t *testing.T) {
	s, ctx, db := fixture(t)
	older, err := s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", IdempotencyKey: "k1", InputHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkWaitingCredits(ctx, older.ID, "balance", 500); err != nil {
		t.Fatal(err)
	}
	// 用户改成取消旧执行并起了一个新版本
	if _, err = db.Exec(`UPDATE generation_runs SET status='cancelled' WHERE id=$1`, older.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, CreateReq{UserID: "u1", ProjectID: "p1", Mode: "revise", IdempotencyKey: "k2", InputHash: "h2"}); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeRun(ctx, older.ID, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("已被替代的旧执行不得被续跑")
	}
}

var _ = context.Background
