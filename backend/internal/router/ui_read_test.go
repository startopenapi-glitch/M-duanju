package router

import (
	"drama-platform/internal/handler"
	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUIReadRoutesPreserveExactIdentityAndNoWrites(t *testing.T) {
	db := testutil.DB(t)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,credits) VALUES('u','u@example.test','x',500),('v','v@example.test','x',0)`,
		`INSERT INTO projects(id,user_id,title,idea) VALUES('p','u','Original','test')`,
		`INSERT INTO generation_runs(id,user_id,project_id,project_version,status,idempotency_key,input_hash) VALUES('11111111111111111111111111111111','u','p',1,'completed','original','secret-hash'),('22222222222222222222222222222222','u','p',2,'queued','new','secret-hash')`,
		`INSERT INTO recharge_orders(id,user_id,amount_yuan,amount_cents,credits,status,credited,run_id,pay_mode,transaction_id) VALUES('o','u',10,1000,1000,'paid',false,'11111111111111111111111111111111','wechat','synthetic-private-transaction')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	r := Setup(handler.New(service.New(db, "synthetic"), "synthetic"), "synthetic", "")
	request := func(path, user string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		if user != "" {
			var version int64
			if err := db.QueryRow(`SELECT session_version FROM users WHERE id=$1`, user).Scan(&version); err != nil {
				t.Fatal(err)
			}
			tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": user, "ver": version, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("synthetic"))
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("cacheable response")
		}
		return w.Code, w.Body.String()
	}
	path := "/api/runs/context?runId=11111111111111111111111111111111"
	code, body := request(path, "u")
	if code != 200 {
		t.Fatalf("exact run route %d %s", code, body)
	}
	var resp struct {
		Data struct {
			Run struct {
				ID string `json:"id"`
			}
			IsLatest bool `json:"isLatest"`
		}
	}
	json.Unmarshal([]byte(body), &resp)
	if resp.Data.Run.ID != "11111111111111111111111111111111" || resp.Data.IsLatest {
		t.Fatal("substituted latest run", body)
	}
	for _, u := range []string{"", "v"} {
		code, _ = request(path, u)
		want := 404
		if u == "" {
			want = 401
		}
		if code != want {
			t.Fatal("scope", u, code)
		}
	}
	for _, p := range []string{"/api/runs/context", "/api/runs/context?runId=no", "/api/runs/context?runId=11111111111111111111111111111111&projectId=p&requestKey=original"} {
		if code, _ = request(p, "u"); code != 400 {
			t.Fatal("invalid selector", p, code)
		}
	}
	code, body = request("/api/runs/context?projectId=p&requestKey=missing", "u")
	if code != 200 || !strings.Contains(body, `"data":null`) {
		t.Fatal("missing-key contract", body)
	}
	for i := 0; i < 3; i++ {
		code, body = request("/api/orders/o/progress", "u")
		if code != 200 || !strings.Contains(body, `"credited":false`) || !strings.Contains(body, `"paymentEvidence":"recorded"`) {
			t.Fatal("unverified credited fact", code, body)
		}
		for _, secret := range []string{"secret-hash", "synthetic-private-transaction", "u@example.test", "input_hash"} {
			if strings.Contains(body, secret) {
				t.Fatal("private data", secret)
			}
		}
	}
	if code, _ = request("/api/orders/o/progress", "v"); code != 404 {
		t.Fatal("foreign order", code)
	}

	t.Run("request key stays within the owned project", func(t *testing.T) {
		for _, tc := range []struct {
			path, user string
			want       int
		}{
			{"/api/runs/context?projectId=p&requestKey=original", "u", 200},
			{"/api/runs/context?projectId=p&requestKey=original", "v", 404},
			{"/api/runs/context?projectId=missing&requestKey=original", "u", 404},
			{"/api/runs/context?runId=33333333333333333333333333333333", "u", 404},
			{"/api/orders/missing/progress", "u", 404},
		} {
			c, b := request(tc.path, tc.user)
			if c != tc.want {
				t.Fatalf("%s %d %s", tc.path, c, b)
			}
			if strings.Contains(b, "secret-hash") || strings.Contains(b, "inputHash") {
				t.Fatal("run private data leaked")
			}
		}
	})
	t.Run("read projection leaves all business rows unchanged", func(t *testing.T) {
		snapshot := func() map[string]string {
			out := map[string]string{}
			for _, table := range []string{"users", "projects", "generation_runs", "jobs", "recharge_orders", "credit_transactions", "resume_wakeups", "refund_intents"} {
				var v string
				if err := db.QueryRow("SELECT COALESCE(json_agg(row_to_json(x))::text,'[]') FROM (SELECT * FROM " + table + " ORDER BY 1) x").Scan(&v); err != nil {
					t.Fatal(err)
				}
				out[table] = v
			}
			return out
		}
		before := snapshot()
		for i := 0; i < 3; i++ {
			request(path, "u")
			request("/api/orders/o/progress", "u")
		}
		after := snapshot()
		for table, v := range before {
			if v != after[table] {
				t.Fatal("readonly mutated", table)
			}
		}
	})
	var credits, runs, tx, wakeups int
	db.QueryRow(`SELECT credits FROM users WHERE id='u'`).Scan(&credits)
	db.QueryRow(`SELECT count(*) FROM generation_runs`).Scan(&runs)
	db.QueryRow(`SELECT count(*) FROM credit_transactions`).Scan(&tx)
	db.QueryRow(`SELECT count(*) FROM resume_wakeups`).Scan(&wakeups)
	if credits != 500 || runs != 2 || tx != 0 || wakeups != 0 {
		t.Fatal("read mutated financial state", credits, runs, tx, wakeups)
	}
	t.Run("legacy refund is not hidden", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE recharge_orders SET refunded_at=now(),refund_amount_cents=100 WHERE id='o'`); err != nil {
			t.Fatal(err)
		}
		c, b := request("/api/orders/o/progress", "u")
		if c != 200 || !strings.Contains(b, `"status":"legacy_unverified"`) {
			t.Fatal(c, b)
		}
	})
	t.Run("inactive account is rejected", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE users SET status='banned' WHERE id='u'`); err != nil {
			t.Fatal(err)
		}
		c, _ := request(path, "u")
		if c != 401 && c != 403 {
			t.Fatal("inactive", c)
		}
		if _, err := db.Exec(`UPDATE users SET status='active' WHERE id='u'`); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("database failure is not null success", func(t *testing.T) {
		if _, err := db.Exec(`ALTER TABLE refund_intents RENAME TO ui_test_refund_unavailable`); err != nil {
			t.Fatal(err)
		}
		c, b := request("/api/orders/o/progress", "u")
		if c != 500 || strings.Contains(b, "ui_test_refund_unavailable") {
			t.Fatal(c, b)
		}
	})

}
