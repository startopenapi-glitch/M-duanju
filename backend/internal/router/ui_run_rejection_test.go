package router

import (
	"drama-platform/internal/handler"
	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestUIRunNotCreatedCodeOnlyBeforeCreation(t *testing.T) {
	db := testutil.DB(t)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,credits) VALUES('u','u@example.test','x',10000)`,
		`INSERT INTO projects(id,user_id,title,idea,total_seconds) VALUES('p','u','Test','synthetic',15)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	r := Setup(handler.New(service.New(db, "synthetic"), "synthetic"), "synthetic", "")
	var version int64
	if err := db.QueryRow(`SELECT session_version FROM users WHERE id='u'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "u", "ver": version, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/projects/p/runs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := post(`{"idempotencyKey":"rejected","mode":"revise","scope":{"instruction":""}}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"run_not_created"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM generation_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatal("rejection created run", n, err)
	}
	const first = `{"idempotencyKey":"accepted","mode":"first","scope":{}}`
	w = post(first)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// A committed run can have a later read failure. It must never be advertised
	// as rejected/not-created or encourage a fresh idempotency key.
	if _, err := db.Exec(`ALTER TABLE jobs RENAME TO unavailable_jobs`); err != nil {
		t.Fatal(err)
	}
	w = post(first)
	if w.Code != 409 || strings.Contains(w.Body.String(), "run_not_created") {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := db.QueryRow(`SELECT count(*) FROM generation_runs`).Scan(&n); err != nil || n != 1 {
		t.Fatal("committed run lost", n, err)
	}
	var credits int
	if err := db.QueryRow(`SELECT credits FROM users WHERE id='u'`).Scan(&credits); err != nil || credits != 10000 {
		t.Fatal("billing changed", credits, err)
	}
}
