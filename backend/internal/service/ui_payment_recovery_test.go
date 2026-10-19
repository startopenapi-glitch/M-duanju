package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOrderProgressRestoresOnlyOwnedLivePaymentWithoutWrites(t *testing.T) {
	s, db := rechargeFixture(t)
	// Keep live fixtures away from age=0: the test DB runs in a VM whose clock
	// can lead the Go process by sub-milliseconds. Future/expired cases below
	// still exercise the strict production time guards unchanged.
	mustExec(t, db, `INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,pay_mode,channel,code_url,h5_url,run_id,created_at) VALUES('recover','u1',100,10000,10000,'pending','wechat','native','weixin://wxpay/bizpayurl?pr=SYNTHETIC','','r1',now()-interval '1 minute')`)
	read := func(user string) map[string]any {
		t.Helper()
		o, e := s.OrderProgress(context.Background(), user, "recover")
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(o)
		var out map[string]any
		json.Unmarshal(b, &out)
		return out
	}
	snapshot := func() string {
		t.Helper()
		var v string
		if e := db.QueryRow(`SELECT row_to_json(o)::text FROM recharge_orders o WHERE id='recover'`).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	before := snapshot()
	for i := 0; i < 3; i++ {
		o := read("u1")
		payment, _ := o["payment"].(map[string]any)
		qr, _ := payment["qr"].(string)
		if !strings.HasPrefix(qr, "data:image/png;base64,") {
			t.Fatal("original native payment missing", o)
		}
		if o["id"] != "recover" || o["amountCents"] != float64(10000) || o["runId"] != "r1" {
			t.Fatal("identity changed", o)
		}
	}
	if snapshot() != before {
		t.Fatal("read changed original order")
	}
	for _, table := range []string{"credit_transactions", "resume_wakeups", "refund_intents"} {
		if countRows(t, db, "SELECT count(*) FROM "+table) != 0 {
			t.Fatal("read wrote", table)
		}
	}
	if _, e := s.OrderProgress(context.Background(), "u2", "recover"); !errors.Is(e, ErrUIReadNotFound) {
		t.Fatal("foreign payment leaked", e)
	}
	for _, tc := range []struct{ name, update string }{
		{"paid", `status='paid'`}, {"credited", `credited=true`}, {"closed", `status='closed'`}, {"failed", `status='failed'`},
		{"channel fact", `transaction_id='synthetic'`}, {"expired", `created_at=now()-interval '31 minutes'`},
		{"future", `created_at=now()+interval '1 hour'`}, {"legacy refund", `refunded_at=now()`},
		{"manual", `pay_mode='manual'`}, {"unsafe URL", `code_url='https://evil.invalid/pay'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustExec(t, db, `UPDATE recharge_orders SET status='pending',credited=false,transaction_id='',created_at=now()-interval '1 minute',refunded_at=NULL,pay_mode='wechat',code_url='weixin://wxpay/bizpayurl?pr=SYNTHETIC' WHERE id='recover'`)
			mustExec(t, db, `UPDATE recharge_orders SET `+tc.update+` WHERE id='recover'`)
			if read("u1")["payment"] != nil {
				t.Fatal("unsafe payment still presented")
			}
		})
	}
	mustExec(t, db, `UPDATE recharge_orders SET code_url='',channel='h5',h5_url='https://wx.tenpay.com/cgi-bin/mmpayweb-bin/checkmweb?prepay_id=SYNTHETIC',created_at=now()-interval '1 minute' WHERE id='recover'`)
	if p, _ := read("u1")["payment"].(map[string]any); p["h5Url"] == nil {
		t.Fatal("live H5 not restored")
	}
	mustExec(t, db, `UPDATE recharge_orders SET created_at=now()-interval '6 minutes' WHERE id='recover'`)
	if read("u1")["payment"] != nil {
		t.Fatal("stale H5 restored")
	}
	mustExec(t, db, `UPDATE recharge_orders SET created_at=now()-interval '1 minute',h5_url='https://wx.tenpay.com.evil.invalid/pay' WHERE id='recover'`)
	if read("u1")["payment"] != nil {
		t.Fatal("foreign H5 host restored")
	}
	mustExec(t, db, `UPDATE users SET status='banned' WHERE id='u1'`)
	if _, e := s.OrderProgress(context.Background(), "u1", "recover"); !errors.Is(e, ErrUIReadNotFound) {
		t.Fatal("inactive payment leaked", e)
	}
}
