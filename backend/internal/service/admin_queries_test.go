package service

import (
	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
	"encoding/json"
	"strings"
	"testing"
)

func queryFixture(t *testing.T) *Service {
	t.Helper()
	s := New(testutil.DB(t), "query-test")
	mustExec(t, s.db, `INSERT INTO users(id,email,password_hash,nickname,credits) VALUES('uq','query@example.com','secret-password',NULL,500)`)
	mustExec(t, s.db, `INSERT INTO projects(id,user_id,idea,title) VALUES('pq','uq','secret-prompt','Query project')`)
	return s
}
func TestAdminQueryOverviewFactsAndErrors(t *testing.T) {
	s := queryFixture(t)
	mustExec(t, s.db, `INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,mock) VALUES('real','uq',10,1001,1000,'paid',false),('mock','uq',99,9900,9900,'paid',true),('pending','uq',88,8800,8800,'pending',false)`)
	mustExec(t, s.db, `INSERT INTO jobs(id,kind,project_id,status,phase,error_code) VALUES('jq','keyvisual','pq','reconcile','submitting','submission_unknown')`)
	got, e := s.AdminOverview()
	if e != nil {
		t.Fatal(e)
	}
	if got["totalRechargedCents"] != int64(1001) || got["unknownJobs"] != int64(1) || got["pendingOrders"] != int64(1) {
		t.Fatalf("untruthful facts: %#v", got)
	}
	mustExec(t, s.db, `DROP TABLE refund_intents`)
	if _, e = s.AdminOverview(); e == nil {
		t.Fatal("missing refund source silently ignored")
	}
}
func TestAdminQueryOrdersFilterStablePageAndDetail(t *testing.T) {
	s := queryFixture(t)
	mustExec(t, s.db, `INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,credited,created_at) VALUES('oa','uq',10,1000,1000,'closed',false,'2026-01-01'),('ob','uq',10,1001,1000,'closed',true,'2026-01-01'),('oc','uq',10,1002,1000,'paid',true,'2026-01-01')`)
	p, e := s.AdminOrders("query@example", "closed", 2, 1)
	if e != nil {
		t.Fatal(e)
	}
	rows := p.Items.([]*model.RechargeOrder)
	if p.Total != 2 || len(rows) != 1 || rows[0].ID != "oa" || rows[0].Status != "closed" {
		t.Fatalf("filter/page = %#v %#v", p, rows)
	}
	d, e := s.AdminOrderDetail("ob")
	if e != nil {
		t.Fatal(e)
	}
	if d["order"].(*model.RechargeOrder).AmountCents != 1001 || d["refund"] != nil || d["run"] != nil {
		t.Fatalf("detail=%#v", d)
	}
	mustExec(t, s.db, `ALTER TABLE recharge_orders RENAME TO unavailable_orders`)
	if _, e = s.AdminOrders("", "", 1, 20); e == nil {
		t.Fatal("order query swallowed source failure")
	}
}
func TestAdminQueryDetailsProtectRunOwnershipAndPrivatePayloads(t *testing.T) {
	s := queryFixture(t)
	mustExec(t, s.db, `INSERT INTO users(id,email,password_hash) VALUES('foreign','foreign@example.com','foreign-password')`)
	mustExec(t, s.db, `INSERT INTO projects(id,user_id,idea) VALUES('foreignp','foreign','foreign prompt')`)
	mustExec(t, s.db, `INSERT INTO generation_runs(id,user_id,project_id,idempotency_key,input_hash) VALUES('foreignr','foreign','foreignp','foreign','hash')`)
	mustExec(t, s.db, `INSERT INTO jobs(id,kind,project_id,status,phase,reserved_credits,run_id,payload,last_error) VALUES('jq','keyvisual','pq','reconcile','submitting',30,'foreignr','{"prompt":"secret-prompt"}','provider response token=super-secret')`)
	if _, e := s.AdminJobDetail("jq"); e == nil {
		t.Fatal("foreign run joined to unrelated job")
	}
	mustExec(t, s.db, `UPDATE jobs SET run_id=NULL WHERE id='jq'`)
	p, e := s.AdminJobsList("Query project", "reconcile", 1, 20)
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 1 {
		t.Fatalf("jobs=%#v", p)
	}
	d, e := s.AdminJobDetail("jq")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "secret-prompt") || strings.Contains(string(b), "super-secret") {
		t.Fatalf("private data leaked: %s", b)
	}
	u, e := s.AdminUserDetail("uq")
	if e != nil {
		t.Fatal(e)
	}
	if u["reservedCredits"] != int64(30) || u["availableCredits"] != int64(470) || u["projectsCount"] != int64(1) {
		t.Fatalf("user detail=%#v", u)
	}
	b, _ = json.Marshal(u)
	if strings.Contains(string(b), "secret-password") {
		t.Fatal("password leaked")
	}
}
func TestAdminQueryCreditsAndAuditAllSources(t *testing.T) {
	s := queryFixture(t)
	mustExec(t, s.db, `INSERT INTO credit_transactions(id,user_id,amount,balance_after,kind,note) VALUES('cq','uq',10,500,'admin_adjust','运营补偿')`)
	mustExec(t, s.db, `INSERT INTO audit_logs(id,user_id,action,note) VALUES('aq','uq','provider_error','raw upstream secret-token prompt')`)
	mustExec(t, s.db, `INSERT INTO partners(id,user_id,name,code) VALUES('partnerq','uq','合作','query-code')`)
	mustExec(t, s.db, `INSERT INTO partner_audit_events(id,partner_id,actor_id,action,detail) VALUES('pa','partnerq','uq','create','{"secret":"do-not-expose"}')`)
	p, e := s.AdminCredits("query@example", 1, 20)
	if e != nil || p.Total != 1 {
		t.Fatalf("credits %v %#v", e, p)
	}
	a, e := s.AdminAudit("", 1, 20)
	if e != nil || a.Total != 2 {
		t.Fatalf("audit %v %#v", e, a)
	}
	b, _ := json.Marshal(a)
	for _, secret := range []string{"secret-token", "do-not-expose"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("audit leaked %s", secret)
		}
	}
}

func TestAdminQueryJobTimelineExactReceiptIdentity(t *testing.T) {
	s := queryFixture(t)
	mustExec(t, s.db, `INSERT INTO shots(id,project_id,shot_code,seq,visual_prompt,status,upstream_id) VALUES('sq','pq','s1',1,'hidden shot prompt','GENERATED','task-q'),('sibling','pq','s2',2,'hidden sibling prompt','GENERATED','task-other')`)
	mustExec(t, s.db, `INSERT INTO jobs(id,project_id,shot_id,kind,status,phase,upstream_id) VALUES('jq','pq','sq','shot','done','done','task-q')`)
	mustExec(t, s.db, `INSERT INTO generation_settlements(id,provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url) VALUES('receiptq','limeng','video','task-q','pq','sq','uq',120000,12,'settled','https://example.com/private?token=secret'),('receiptother','limeng','video','task-other','pq','sibling','uq',300000,30,'settled','private')`)
	d, e := s.AdminJobDetail("jq")
	if e != nil {
		t.Fatal(e)
	}
	shots := d["shots"].([]AdminShot)
	receipts := d["receipts"].([]AdminReceipt)
	if len(shots) != 1 || shots[0].ID != "sq" || len(receipts) != 1 || receipts[0].ID != "receiptq" || receipts[0].Credits != 12 {
		t.Fatalf("unrelated timeline: %#v", d)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "hidden shot prompt") || strings.Contains(string(b), "token=secret") {
		t.Fatal("timeline leaked prompt or receipt URL")
	}
	mustExec(t, s.db, `UPDATE jobs SET kind='assemble',shot_id=NULL,upstream_id=NULL WHERE id='jq'`)
	d, e = s.AdminJobDetail("jq")
	if e != nil {
		t.Fatal(e)
	}
	if len(d["shots"].([]AdminShot)) != 2 || len(d["receipts"].([]AdminReceipt)) != 2 {
		t.Fatalf("assembly timeline incomplete: %#v", d)
	}
}
