package service

import (
	"drama-platform/internal/testutil"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAdminRefundUnconfiguredNeverClaimsSuccess(t *testing.T) {
	t.Setenv("WXPAY_APPID", "")
	t.Setenv("WXPAY_MOCK", "0")
	db := testutil.DB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('refund-u','refund@example.invalid','unused',1000);
 INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,channel,pay_mode,out_trade_no,credited) VALUES('refund-o','refund-u',10,1000,1000,'paid','native','native','trade-refund',true)`)
	svc := New(db, "test")
	_, err := svc.RefundOrder(&RefundRequest{OrderID: "refund-o", AdminID: "refund-u", AmountCents: 1000, Reason: "test"})
	if err == nil {
		t.Fatal("unconfigured refund falsely reported success")
	}
	var balance int64
	if err = db.QueryRow(`SELECT credits FROM users WHERE id='refund-u'`).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 1000 {
		t.Fatalf("balance changed without verified payment: %d", balance)
	}
}

type adminRefundClient struct {
	submit func(string, string, int64, int64, string) (*RefundResponse, error)
	query  func(string) (*RefundResponse, error)
}

func (c *adminRefundClient) SubmitRefund(a, b string, d, e int64, f string) (*RefundResponse, error) {
	return c.submit(a, b, d, e, f)
}
func (c *adminRefundClient) LookupRefund(a string) (*RefundResponse, error) { return c.query(a) }
func adminRefundResponse(no, status string) *RefundResponse {
	r := &RefundResponse{RefundID: "provider-refund-1", OutRefundNo: no, OutTradeNo: "trade-refund", Status: status}
	r.Amount.Total = 1000
	r.Amount.Refund = 600
	r.Amount.Currency = "CNY"
	return r
}
func adminRefundFixture(t *testing.T, c RefundClient) *Service {
	db := testutil.DB(t)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('refund-u','refund@example.invalid','unused',1000);
 INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,channel,pay_mode,out_trade_no,credited) VALUES('refund-o','refund-u',10,1000,1000,'paid','native','native','trade-refund',true)`)
	return New(db, "test", WithRefundClient(c))
}
func adminRefundBalance(t *testing.T, s *Service, want int64) {
	t.Helper()
	var got int64
	if e := s.db.QueryRow(`SELECT credits FROM users WHERE id='refund-u'`).Scan(&got); e != nil {
		t.Fatal(e)
	}
	if got != want {
		t.Fatalf("balance %d, want %d", got, want)
	}
}
func TestAdminRefundConcurrentIntentAndQueryDebitOnce(t *testing.T) {
	started, release := make(chan string, 1), make(chan struct{})
	var calls atomic.Int64
	c := &adminRefundClient{}
	svc := adminRefundFixture(t, c)
	c.submit = func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
		calls.Add(1)
		// Observable durable intent MUST exist while transport is blocked.
		r, e := svc.GetRefund("refund-o")
		if e != nil || r == nil || r.RefundNo != no || r.Status != "submitting" {
			return nil, fmt.Errorf("intent missing before POST: %v", e)
		}
		started <- no
		<-release
		return adminRefundResponse(no, "PROCESSING"), nil
	}
	c.query = func(no string) (*RefundResponse, error) { return adminRefundResponse(no, "SUCCESS"), nil }
	done := make(chan error, 1)
	go func() {
		r, e := svc.BeginRefund("refund-o", "refund-u", 600, "customer request")
		if e == nil && r.Status != "processing" {
			e = fmt.Errorf("status %s", r.Status)
		}
		done <- e
	}()
	no := <-started
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := svc.BeginRefund("refund-o", "refund-u", 600, "customer request")
			if e == nil && (r.RefundNo != no || r.Status != "submitting") {
				e = fmt.Errorf("different intent: %+v", r)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate POSTs: %d", calls.Load())
	}
	adminRefundBalance(t, svc, 1000)
	if _, e := svc.BeginRefund("refund-o", "refund-u", 601, "customer request"); !errors.Is(e, ErrRefundConflict) {
		t.Fatalf("conflict %v", e)
	}
	errs = make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := svc.QueryRefund("refund-o", "refund-u")
			if e == nil && (r.Status != "succeeded" || r.CreditsRevoked != 600) {
				e = fmt.Errorf("bad result: %+v", r)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	adminRefundBalance(t, svc, 400)
	if n := countRows(t, svc.db, `SELECT count(*) FROM credit_transactions WHERE kind='refund'`); n != 1 {
		t.Fatalf("ledger duplicates %d", n)
	}
	if n := countRows(t, svc.db, `SELECT count(*) FROM audit_logs WHERE action='refund_result' AND to_status='succeeded'`); n != 1 {
		t.Fatalf("audit duplicates %d", n)
	}
}
func TestAdminRefundUnknownAndIdentityMismatchNeverDebit(t *testing.T) {
	for _, kind := range []string{"timeout", "nil", "order", "amount", "currency", "refundNo", "refundID", "transaction", "malformed", "abnormal", "emptyStatus"} {
		t.Run(kind, func(t *testing.T) {
			c := &adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
				r := adminRefundResponse(no, "SUCCESS")
				switch kind {
				case "timeout":
					return nil, errors.New("transport lost")
				case "nil":
					return nil, nil
				case "order":
					r.OutTradeNo = "foreign"
				case "amount":
					r.Amount.Refund = 599
				case "currency":
					r.Amount.Currency = "USD"
				case "refundNo":
					r.OutRefundNo = "foreign"
				case "refundID":
					r.RefundID = ""
				case "transaction":
					r.TransactionID = "foreign"
				case "malformed":
					return decodeRefund([]byte(`not-json`), nil)
				case "abnormal":
					r.Status = "ABNORMAL"
				case "emptyStatus":
					r.Status = ""
				}
				return r, nil
			}, query: func(no string) (*RefundResponse, error) {
				r := adminRefundResponse(no, "SUCCESS")
				r.TransactionID = "paid-transaction"
				return r, nil
			}}
			s := adminRefundFixture(t, c)
			if kind == "transaction" {
				mustExec(t, s.db, `UPDATE recharge_orders SET transaction_id='paid-transaction'`)
			}
			r, e := s.BeginRefund("refund-o", "refund-u", 600, "test")
			if e != nil || r.Status != "unknown" {
				t.Fatalf("result %+v %v", r, e)
			}
			adminRefundBalance(t, s, 1000)
			if n := countRows(t, s.db, `SELECT count(*) FROM recharge_orders WHERE refunded_at IS NOT NULL`); n != 0 {
				t.Fatal("marked refunded before proof")
			}
			r, e = s.QueryRefund("refund-o", "refund-u")
			if e != nil || r.Status != "succeeded" {
				t.Fatalf("recovery %+v %v", r, e)
			}
			adminRefundBalance(t, s, 400)
		})
	}
}
func TestAdminRefundProtectsReservationsAndRecordsShortfall(t *testing.T) {
	for _, balance := range []int{0, 200, 1000} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			c := &adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
				return adminRefundResponse(no, "SUCCESS"), nil
			}}
			s := adminRefundFixture(t, c)
			mustExec(t, s.db, `UPDATE users SET credits=`+fmt.Sprint(balance)+`; INSERT INTO projects(id,user_id,idea,budget_credits) VALUES('refund-p','refund-u','synthetic',1000);
  INSERT INTO jobs(id,kind,project_id,status,phase,reserved_credits) VALUES('refund-j','keyvisual','refund-p','reconcile','submitting',800)`)
			r, e := s.BeginRefund("refund-o", "refund-u", 600, "test")
			if e != nil {
				t.Fatal(e)
			}
			wantRevoke := int64(0)
			if balance == 1000 {
				wantRevoke = 200
			}
			if r.CreditsRevoked != wantRevoke || r.CreditShortfall != 600-wantRevoke {
				t.Fatalf("shortfall %+v", r)
			}
			adminRefundBalance(t, s, int64(balance)-wantRevoke)
			if n := countRows(t, s.db, `SELECT count(*) FROM jobs WHERE id='refund-j' AND reserved_credits=800`); n != 1 {
				t.Fatal("reservation changed")
			}
		})
	}
}
func TestAdminRefundCrashBeforePostQueriesWithoutResubmit(t *testing.T) {
	var posts atomic.Int64
	c := &adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
		posts.Add(1)
		return nil, errors.New("must not POST")
	}, query: func(no string) (*RefundResponse, error) { return adminRefundResponse(no, "SUCCESS"), nil }}
	s := adminRefundFixture(t, c)
	mustExec(t, s.db, `INSERT INTO refund_intents(order_id,refund_no,status,amount_cents,reason,admin_id) VALUES('refund-o','RF-crashed','submitting',600,'test','refund-u')`)
	r, e := s.BeginRefund("refund-o", "refund-u", 600, "test")
	if e != nil || r.RefundNo != "RF-crashed" {
		t.Fatalf("existing %+v %v", r, e)
	}
	r, e = s.QueryRefund("refund-o", "refund-u")
	if e != nil || r.Status != "succeeded" {
		t.Fatalf("query %+v %v", r, e)
	}
	if posts.Load() != 0 {
		t.Fatal("rePOST after crash")
	}
}
func TestAdminRefundAtomicApplicationFailureCanRequery(t *testing.T) {
	c := &adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
		return adminRefundResponse(no, "SUCCESS"), nil
	}, query: func(no string) (*RefundResponse, error) { return adminRefundResponse(no, "SUCCESS"), nil }}
	s := adminRefundFixture(t, c)
	mustExec(t, s.db, `ALTER TABLE audit_logs ADD CONSTRAINT reject_refund_success CHECK(to_status <> 'succeeded')`)
	if _, e := s.BeginRefund("refund-o", "refund-u", 600, "test"); e == nil {
		t.Fatal("expected audit transaction failure")
	}
	adminRefundBalance(t, s, 1000)
	if n := countRows(t, s.db, `SELECT count(*) FROM credit_transactions WHERE kind='refund'`); n != 0 {
		t.Fatal("partial ledger commit")
	}
	if n := countRows(t, s.db, `SELECT count(*) FROM recharge_orders WHERE refunded_at IS NOT NULL`); n != 0 {
		t.Fatal("partial order commit")
	}
	mustExec(t, s.db, `ALTER TABLE audit_logs DROP CONSTRAINT reject_refund_success`)
	r, e := s.QueryRefund("refund-o", "refund-u")
	if e != nil || r.Status != "succeeded" {
		t.Fatalf("query %+v %v", r, e)
	}
	adminRefundBalance(t, s, 400)
}
func TestAdminRefundLegacyMigrationRerunNeverResends(t *testing.T) {
	db := testutil.DBThrough(t, "019z")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('refund-u','refund@example.invalid','unused',1000);
 INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,channel,out_trade_no,credited,refunded_at,refund_amount_cents,refund_no) VALUES('refund-o','refund-u',10,1000,1000,'paid','native','trade-refund',true,now(),600,'old-refund')`)
	migration, e := os.ReadFile("../../migrations/020_refund_lifecycle.sql")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		mustExec(t, db, string(migration))
	}
	s := New(db, "test", WithRefundClient(&adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
		t.Fatal("legacy rePOST")
		return nil, nil
	}}))
	r, e := s.GetRefund("refund-o")
	if e != nil || r == nil || r.Status != "unknown" || !r.legacy {
		t.Fatalf("legacy %+v %v", r, e)
	}
	if _, e = s.BeginRefund("refund-o", "refund-u", 600, "test"); !errors.Is(e, ErrRefundLegacy) {
		t.Fatal(e)
	}
	if _, e = s.QueryRefund("refund-o", "refund-u"); !errors.Is(e, ErrRefundLegacy) {
		t.Fatal(e)
	}
	adminRefundBalance(t, s, 1000)
}

func TestAdminRefundRejectsChangedProviderIdentityAndClosedIsNotPaid(t *testing.T) {
	for _, kind := range []string{"changedIdentity", "closed"} {
		t.Run(kind, func(t *testing.T) {
			c := &adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
				return adminRefundResponse(no, "PROCESSING"), nil
			}, query: func(no string) (*RefundResponse, error) {
				r := adminRefundResponse(no, "SUCCESS")
				if kind == "changedIdentity" {
					r.RefundID = "different-refund"
				} else {
					r.Status = "CLOSED"
				}
				return r, nil
			}}
			s := adminRefundFixture(t, c)
			if _, e := s.BeginRefund("refund-o", "refund-u", 600, "test"); e != nil {
				t.Fatal(e)
			}
			r, e := s.QueryRefund("refund-o", "refund-u")
			want := "unknown"
			if kind == "closed" {
				want = "failed"
			}
			if e != nil || r.Status != want {
				t.Fatalf("result %+v %v", r, e)
			}
			adminRefundBalance(t, s, 1000)
		})
	}
}
func TestAdminRefundValidationNoTransportOrIntent(t *testing.T) {
	for _, kind := range []string{"zero", "negative", "over", "blankReason", "unpaid", "uncredited", "manual", "mock", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			c := &adminRefundClient{submit: func(_, no string, _, _ int64, _ string) (*RefundResponse, error) {
				t.Fatal("invalid request sent POST")
				return nil, nil
			}}
			s := adminRefundFixture(t, c)
			amount := int64(600)
			reason := "test"
			switch kind {
			case "zero":
				amount = 0
			case "negative":
				amount = -1
			case "over":
				amount = 1001
			case "blankReason":
				reason = " "
			case "unpaid":
				mustExec(t, s.db, `UPDATE recharge_orders SET status='pending'`)
			case "uncredited":
				mustExec(t, s.db, `UPDATE recharge_orders SET credited=false`)
			case "manual":
				mustExec(t, s.db, `UPDATE recharge_orders SET channel='manual'`)
			case "mock":
				mustExec(t, s.db, `UPDATE recharge_orders SET mock=true`)
			case "legacy":
				mustExec(t, s.db, `UPDATE recharge_orders SET refunded_at=now()`)
			}
			if _, e := s.BeginRefund("refund-o", "refund-u", amount, reason); e == nil {
				t.Fatal("invalid request accepted")
			}
			if n := countRows(t, s.db, `SELECT count(*) FROM refund_intents`); n != 0 {
				t.Fatal("invalid intent created")
			}
			adminRefundBalance(t, s, 1000)
		})
	}
}

func TestAdminRefundLegacyMigrationHandlesMaxLengthIDAndUnknownAmount(t *testing.T) {
	db := testutil.DBThrough(t, "019z")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('refund-u','refund@example.invalid','unused',1000);
 INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,refunded_at,refund_amount_cents) VALUES(repeat('x',64),'refund-u',10,1000,1000,'paid',now(),0)`)
	migration, e := os.ReadFile("../../migrations/020_refund_lifecycle.sql")
	if e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, string(migration))
	var amount int64
	if e = db.QueryRow(`SELECT amount_cents FROM refund_intents`).Scan(&amount); e != nil {
		t.Fatal(e)
	}
	if amount != 0 {
		t.Fatalf("invented legacy amount %d", amount)
	}
}
