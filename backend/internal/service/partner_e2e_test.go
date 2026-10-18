package service

import (
	"context"
	"testing"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

// 端到端（服务层）：签名候选 → 注册 → 归属落库。
// 证明候选 cookie 的签发/校验与服务内建号绑定是同一条链，而不是各自单独可用。
func TestRegisterBindsAttributionFromSignedCandidate(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic-test-secret")
	ctx := context.Background()

	// 代理账号（先正常注册，不带来源）
	owner, err := svc.CreateUser(&model.RegisterReq{Email: "owner@example.invalid", Password: "secret123"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.PartnerStore().Create(ctx, owner.ID, owner.ID, "合作一号", "简介", "wx1")
	if err != nil {
		t.Fatal(err)
	}

	// 推广页下发的候选值（签名）
	raw := svc.PartnerSignCandidate(p.Code)
	if raw == "" {
		t.Fatal("候选值不应为空")
	}
	code, err := svc.PartnerVerifyCandidate(raw)
	if err != nil || code != p.Code {
		t.Fatalf("候选校验 = %q, %v; want %q", code, err, p.Code)
	}

	// 新客户带该候选注册
	buyer, err := svc.CreateUser(&model.RegisterReq{
		Email: "buyer@example.invalid", Password: "secret123", PartnerCode: code,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := svc.PartnerStore().AttributedPartnerID(ctx, buyer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pid != p.ID {
		t.Fatalf("注册后归属 = %q, want %q", pid, p.ID)
	}

	// 篡改的候选不应产生归属
	tampered := raw[:len(raw)-2] + "xx"
	badBuyer, err := svc.CreateUser(&model.RegisterReq{
		Email: "bad@example.invalid", Password: "secret123", PartnerCode: tampered,
	})
	if err != nil {
		t.Fatalf("篡改候选不应阻断注册: %v", err)
	}
	if pid, _ := svc.PartnerStore().AttributedPartnerID(ctx, badBuyer.ID); pid != "" {
		t.Fatalf("篡改候选不应产生归属，实际 %q", pid)
	}
}
