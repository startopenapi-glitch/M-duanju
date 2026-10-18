package router

import (
	"bytes"
	"drama-platform/internal/handler"
	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdminWorkspaceHTTPRolePagingAndMutations(t *testing.T) {
	db := testutil.DB(t)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.test','unused','admin'),('ordinary','ordinary@example.test','unused','user'),('partner','partner@example.test','unused','user'),('suspended','suspended@example.test','unused','user')`,
		`INSERT INTO partners(id,user_id,name,code,status) VALUES('p','partner','Partner','CODE','active'),('s','suspended','Suspended','SUSP','suspended')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	r := Setup(handler.New(service.New(db, "synthetic"), "synthetic"), "synthetic", "")
	token := func(id string) string {
		v, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": id, "ver": 1, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("synthetic"))
		return v
	}
	request := func(method, path, body, id string) (int, map[string]interface{}) {
		q := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		q.Header.Set("Content-Type", "application/json")
		if id != "" {
			q.Header.Set("Authorization", "Bearer "+token(id))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private response cacheable")
		}
		var v map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &v)
		return w.Code, v
	}
	paths := []string{"/overview", "/orders", "/jobs/list", "/users", "/credits", "/audit", "/system", "/partners"}
	for _, p := range paths {
		for _, id := range []string{"", "ordinary", "partner"} {
			status, _ := request("GET", "/api/admin"+p, "", id)
			want := 403
			if id == "" {
				want = 401
			}
			if status != want {
				t.Fatalf("%s %s=%d", p, id, status)
			}
		}
		status, v := request("GET", "/api/admin"+p, "", "admin")
		if status != 200 {
			t.Fatalf("admin GET %s=%d %v", p, status, v)
		}
	}
	if status, v := request("GET", "/api/partner/me", "", "ordinary"); status != 200 || v["data"] != nil {
		t.Fatal(status, v)
	}
	if status, v := request("GET", "/api/partner/me", "", "suspended"); status != 200 || len(v["data"].(map[string]interface{})) != 2 {
		t.Fatal(status, v)
	}
	for _, p := range []string{"/customers", "/orders"} {
		if status, v := request("GET", "/api/partner"+p, "", "partner"); status != 200 || v["data"].(map[string]interface{})["total"] != float64(0) {
			t.Fatal(status, v)
		}
		if status, _ := request("GET", "/api/partner"+p, "", "suspended"); status != 403 {
			t.Fatal(status)
		}
	}
	rid := uuid.NewString()
	body := `{"userId":"ordinary","amount":100,"note":"test","requestId":"` + rid + `"}`
	for i := 0; i < 2; i++ {
		if status, v := request("POST", "/api/admin/users/credits", body, "admin"); status != 200 {
			t.Fatal(status, v)
		}
	}
	if status, _ := request("POST", "/api/admin/users/credits", `{"userId":"ordinary","amount":1.1,"note":"test","requestId":"`+uuid.NewString()+`"}`, "admin"); status != 400 {
		t.Fatal("fractional amount accepted", status)
	}
	if status, v := request("GET", "/api/admin/users/ordinary/detail", "", "admin"); status != 200 || v["data"].(map[string]interface{})["availableCredits"] != float64(100) {
		t.Fatal(status, v)
	}
	if status, v := request("POST", "/api/admin/users/ordinary/status", `{"status":"banned","version":1,"reason":"test"}`, "admin"); status != 200 {
		t.Fatal(status, v)
	}
	if status, _ := request("GET", "/api/auth/me", "", "ordinary"); status != 401 {
		t.Fatal("old session still valid", status)
	}
	if status, _ := request("POST", "/api/admin/orders/missing/refund", `{"amountCents":100,"reason":"test"}`, "ordinary"); status != 401 {
		t.Fatal("inactive account mutation", status)
	}
	if status, _ := request("GET", "/api/admin/orders/missing/detail", "", "admin"); status != 404 {
		t.Fatal("missing order", status)
	}
}
