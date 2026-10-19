package service

import (
	"database/sql"
	"drama-platform/internal/model"
	"errors"
	"github.com/google/uuid"
	"sync"
	"testing"
)

func TestAdminAdjustCannotConsumeReservedCredits(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.com','unused','admin')`)
	if _, e := s.EnqueueJob(JobShot, "p", "s", 1, ""); e != nil {
		t.Fatal(e)
	}
	if e := s.AdjustCredits("admin", &model.AdjustCreditsReq{UserID: "u", Amount: -901, Note: "test", RequestID: uuid.NewString()}); e == nil {
		t.Fatal("adjustment consumed reserved credits")
	}
	var balance int64
	if e := db.QueryRow(`SELECT credits FROM users WHERE id='u'`).Scan(&balance); e != nil || balance != 1000 {
		t.Fatalf("balance changed %d %v", balance, e)
	}
}
func TestAdminManualApprovalRejectsChannelOrder(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.com','unused','admin'); INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,channel,pay_mode,out_trade_no) VALUES('o','u',1,100,100,'pending','native','wxpay','test-order')`)
	if e := s.HandleOrder("admin", "o", &model.HandleOrderReq{Approve: true}); e == nil {
		t.Fatal("manual approval bypassed payment channel")
	}
}

func TestAdminAdjustmentReplayConflictAndAtomicAudit(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.com','unused','admin')`)
	req := model.AdjustCreditsReq{UserID: "u", Amount: 50, Note: "verified adjustment", RequestID: uuid.NewString()}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.AdjustCredits("admin", &req) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var balance, ledger, audit int
	if e := db.QueryRow(`SELECT credits,(SELECT count(*) FROM credit_transactions),(SELECT count(*) FROM audit_logs WHERE action='credits_adjust') FROM users WHERE id='u'`).Scan(&balance, &ledger, &audit); e != nil || balance != 1050 || ledger != 1 || audit != 1 {
		t.Fatalf("replay %d %d %d %v", balance, ledger, audit, e)
	}
	req.Amount = 100
	if e := s.AdjustCredits("admin", &req); !errors.Is(e, ErrAdminConflict) {
		t.Fatalf("conflict %v", e)
	}
	mustExec(t, db, `ALTER TABLE audit_logs ADD CONSTRAINT no_adjust CHECK(action<>'credits_adjust') NOT VALID`)
	req.RequestID = uuid.NewString()
	if e := s.AdjustCredits("admin", &req); e == nil {
		t.Fatal("audit failure accepted")
	}
	if e := db.QueryRow(`SELECT credits FROM users WHERE id='u'`).Scan(&balance); e != nil || balance != 1050 {
		t.Fatal(balance, e)
	}
}
func TestAdminStatusRevokeRestoreAndProtection(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.com','unused','admin')`)
	if _, e := s.SetUserStatus("admin", "admin", "banned", "test", 1); e == nil {
		t.Fatal("self disabled")
	}
	v, e := s.SetUserStatus("admin", "u", "banned", "test", 1)
	if e != nil || v.Version != 2 {
		t.Fatal(v, e)
	}
	if _, e = s.CurrentSession("u", 1); e == nil {
		t.Fatal("old session valid")
	}
	if _, e = s.SetUserStatus("admin", "u", "active", "test", 1); !errors.Is(e, ErrAdminConflict) {
		t.Fatal(e)
	}
	v, e = s.SetUserStatus("admin", "u", "active", "test", 2)
	if e != nil || v.Version != 3 {
		t.Fatal(v, e)
	}
	if _, e = s.CurrentSession("u", 1); e == nil {
		t.Fatal("old session revived")
	}
	if _, e = s.CurrentSession("u", 3); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `ALTER TABLE audit_logs ADD CONSTRAINT no_status CHECK(action<>'user_status') NOT VALID`)
	if _, e = s.SetUserStatus("admin", "u", "banned", "test", 3); e == nil {
		t.Fatal("missing audit allowed")
	}
	if _, e = s.CurrentSession("u", 3); e != nil {
		t.Fatal("failed change revoked session", e)
	}
}
func TestAdminDisabledLegacyReadyJobCannotSubmit(t *testing.T) {
	s, db := queueFixture(t)
	id, e := s.EnqueueJob(JobShot, "p", "s", 1, "")
	if e != nil {
		t.Fatal(e)
	}
	j := claimOne(t, s, JobShot)
	mustExec(t, db, `UPDATE users SET status='banned' WHERE id='u'`)
	// This calls only the atomic local authorization boundary, never a provider.
	e = s.withJob(j).jobMutation(j, func(tx *sql.Tx) error { return s.authorizeSubmissionTx(tx, j) })
	if !errors.Is(e, errAccountPaused) {
		t.Fatalf("legacy disabled task still authorized: %v", e)
	}
	var reserve int
	var phase string
	if e = db.QueryRow(`SELECT phase,reserved_credits FROM jobs WHERE id=$1`, id).Scan(&phase, &reserve); e != nil || phase != "ready" || reserve != 200 {
		t.Fatalf("reservation damaged %s %d %v", phase, reserve, e)
	}
}

func TestAdminWorkerHealthIsObservedNotAssumed(t *testing.T) {
	s, db, _ := settlementFixture(t)
	t.Setenv("WXPAY_APPID", "")
	t.Setenv("WXPAY_MOCK", "")
	v, e := s.AdminSystem()
	if e != nil {
		t.Fatal(e)
	}
	if v["worker"].(map[string]interface{})["status"] != "unknown" {
		t.Fatal("unobserved worker healthy")
	}
	if e = s.RecordWorkerHeartbeat(); e != nil {
		t.Fatal(e)
	}
	v, e = s.AdminSystem()
	if e != nil || v["worker"].(map[string]interface{})["status"] != "healthy" {
		t.Fatal(v, e)
	}
	mustExec(t, db, `UPDATE worker_heartbeats SET last_seen=now()-interval '5 minutes'`)
	v, e = s.AdminSystem()
	if e != nil || v["worker"].(map[string]interface{})["status"] != "stale" {
		t.Fatal(v, e)
	}
}

func TestAdminAuditShowsBoundedStructuredOperationalReason(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.com','unused','admin')`)
	if _, e := s.SetUserStatus("admin", "u", "banned", "customer request", 1); e != nil {
		t.Fatal(e)
	}
	list, e := s.AdminAudit("user_status", 1, 20)
	if e != nil {
		t.Fatal(e)
	}
	entries := list.Items.([]*model.AuditLog)
	if len(entries) != 1 || entries[0].Note != "目标：u；原因：customer request" {
		t.Fatalf("unusable audit %#v", entries)
	}
}

func TestAdminManualOrderTimelineIncludesAudit(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.com','unused','admin'); INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,channel) VALUES('o','u',1,100,100,'pending','manual')`)
	if e := s.HandleOrder("admin", "o", &model.HandleOrderReq{Approve: true, Note: "verified manual receipt"}); e != nil {
		t.Fatal(e)
	}
	d, e := s.AdminOrderDetail("o")
	if e != nil {
		t.Fatal(e)
	}
	ev := d["events"].([]*model.AuditLog)
	if len(ev) != 1 || ev[0].Action != "manual_order_approve" {
		t.Fatalf("manual audit missing from order %#v", ev)
	}
}
