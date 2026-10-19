package service

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type adminRefundRoundTrip func(*http.Request) (*http.Response, error)

func (f adminRefundRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestAdminRefundWechatTransportUsesStableIdentityWithoutNetwork(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var paths []string
	w := &WechatPay{PrivateKey: key, MchID: "test", SerialNo: "test", HTTP: &http.Client{Transport: adminRefundRoundTrip(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method == "POST" {
			var p map[string]interface{}
			if e := json.NewDecoder(r.Body).Decode(&p); e != nil {
				t.Fatal(e)
			}
			a := p["amount"].(map[string]interface{})
			if p["out_trade_no"] != "trade-refund" || p["out_refund_no"] != "RFstable" || a["refund"] != float64(600) || a["total"] != float64(1000) || a["currency"] != "CNY" {
				t.Fatalf("incorrect payload %+v", p)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"refund_id":"provider","out_refund_no":"RFstable","out_trade_no":"trade-refund","status":"PROCESSING","amount":{"refund":600,"total":1000,"currency":"CNY"}}`)), Header: make(http.Header)}, nil
	})}}
	r, e := w.SubmitRefund("trade-refund", "RFstable", 1000, 600, "test")
	if e != nil || r.Status != "PROCESSING" {
		t.Fatalf("submit %+v %v", r, e)
	}
	if _, e = w.LookupRefund("RFstable"); e != nil {
		t.Fatal(e)
	}
	if len(paths) != 2 || paths[0] != "POST /v3/refund/domestic/refunds" || paths[1] != "GET /v3/refund/domestic/refunds/RFstable" {
		t.Fatalf("paths %v", paths)
	}
	w.Mock = true
	if _, e = w.SubmitRefund("trade-refund", "RFstable", 1000, 600, "test"); e == nil {
		t.Fatal("mock accepted")
	}
	if _, e = w.LookupRefund("RFstable"); e == nil {
		t.Fatal("mock query accepted")
	}
	if len(paths) != 2 {
		t.Fatal("mock made outbound call")
	}
}
