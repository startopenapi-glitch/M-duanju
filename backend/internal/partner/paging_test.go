package partner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPartnerPagesStableIsolationAndMasking(t *testing.T) {
	s, ctx, mk := fixture(t)
	for _, id := range []string{"ownerA", "ownerB", "ua", "ub", "other"} {
		mk(id)
	}
	a, e := s.Create(ctx, "admin", "ownerA", "甲", "", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, "admin", "ownerB", "乙", "", "")
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"ua", "ub"} {
		if e = bind(t, s, ctx, id, a.Code); e != nil {
			t.Fatal(e)
		}
	}
	if e = bind(t, s, ctx, "other", b.Code); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE partner_attributions SET attributed_at='2026-01-01'`); e != nil {
		t.Fatal(e)
	}
	p, e := s.CustomersPage(ctx, a.ID, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	q, e := s.CustomersPage(ctx, a.ID, 2, 1)
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 2 || len(p.Items.([]Customer)) != 1 || p.Items.([]Customer)[0].Alias == q.Items.([]Customer)[0].Alias {
		t.Fatalf("customer pages=%#v %#v", p, q)
	}
	for _, id := range []string{"oa", "ob"} {
		if _, e = s.db.Exec(`INSERT INTO recharge_orders(id,user_id,amount_yuan,credits,status) VALUES($1,'ua',10,1000,'paid')`, id); e != nil {
			t.Fatal(e)
		}
		if _, e = s.db.Exec(`INSERT INTO partner_order_attributions(id,order_id,partner_id) VALUES($1,$1,$2)`, id, a.ID); e != nil {
			t.Fatal(e)
		}
	}
	o, e := s.OrdersPage(ctx, a.ID, 2, 1)
	if e != nil || o.Total != 2 || len(o.Items.([]OrderRow)) != 1 {
		t.Fatalf("orders=%#v %v", o, e)
	}
	hidden, e := s.OrdersPage(ctx, b.ID, 1, 20)
	if e != nil || hidden.Total != 0 {
		t.Fatalf("scope=%#v %v", hidden, e)
	}
	buf, _ := json.Marshal(p)
	if strings.Contains(string(buf), "@example") {
		t.Fatal("customer email exposed")
	}
	partners, e := s.ListPage(ctx, "甲", "active", 1, 1)
	if e != nil || partners.Total != 1 {
		t.Fatalf("partner filter=%#v %v", partners, e)
	}
	if _, e = s.db.Exec(`DROP TABLE partner_order_attributions`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.OrdersPage(ctx, a.ID, 1, 20); e == nil {
		t.Fatal("orders source failure swallowed")
	}
}
