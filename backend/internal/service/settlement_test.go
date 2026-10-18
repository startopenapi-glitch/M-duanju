package service

import (
	"database/sql"
	"drama-platform/internal/testutil"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func settlementFixture(t *testing.T) (*Service, *sql.DB, GenerationSettlement) {
	t.Helper()
	db := testutil.DB(t)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,credits) VALUES('u','settlement@example.invalid','unused',1000)`,
		`INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('p','u','synthetic',1000,'SHOTS_GENERATING')`,
		`INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id) VALUES('s','p','S01',1,'QUEUED','task-1')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return New(db, "synthetic-test-secret"), db, GenerationSettlement{Kind: "video", TaskID: "task-1", ProjectID: "p", ShotID: "s", URL: "http://127.0.0.1/synthetic.mp4", ChargeMicros: 2000000, Duration: 5, Width: 1344, Height: 768}
}
func assertSettlementTotals(t *testing.T, db *sql.DB, bal, spent int64, rows int) {
	t.Helper()
	var a, b int64
	var c int
	if e := db.QueryRow(`SELECT credits FROM users WHERE id='u'`).Scan(&a); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT spent_credits FROM projects WHERE id='p'`).Scan(&b); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT count(*) FROM credit_transactions WHERE kind='spend'`).Scan(&c); e != nil {
		t.Fatal(e)
	}
	if a != bal || b != spent || c != rows {
		t.Fatalf("balance=%d spent=%d rows=%d; want %d/%d/%d", a, b, c, bal, spent, rows)
	}
}
func TestGenerationSettlementConcurrentAndReplay(t *testing.T) {
	s, db, in := settlementFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.SettleGeneration(in); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	assertSettlementTotals(t, db, 800, 200, 1)
	fresh := New(db, "synthetic-test-secret")
	if _, e := fresh.SettleGeneration(in); e != nil {
		t.Fatal(e)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestGenerationSettlementFundsAndBudgetStayPending(t *testing.T) {
	for _, kind := range []string{"balance", "budget"} {
		t.Run(kind, func(t *testing.T) {
			s, db, in := settlementFixture(t)
			q := `UPDATE users SET credits=100 WHERE id='u'`
			if kind == "budget" {
				q = `UPDATE projects SET budget_credits=100 WHERE id='p'`
			}
			if _, e := db.Exec(q); e != nil {
				t.Fatal(e)
			}
			_, e := s.SettleGeneration(in)
			var pending *SettlementPendingError
			if !errors.As(e, &pending) {
				t.Fatalf("want pending error, got %v", e)
			}
			var st string
			if e := db.QueryRow(`SELECT status FROM shots WHERE id='s'`).Scan(&st); e != nil {
				t.Fatal(e)
			}
			if st != "QUEUED" {
				t.Fatalf("unsettled shot status %s", st)
			}
			bal := int64(1000)
			if kind == "balance" {
				bal = 100
			}
			assertSettlementTotals(t, db, bal, 0, 0)
			if _, e := db.Exec(`UPDATE users SET credits=1000 WHERE id='u'; UPDATE projects SET budget_credits=1000 WHERE id='p'`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SettleGeneration(in); e != nil {
				t.Fatal(e)
			}
			assertSettlementTotals(t, db, 800, 200, 1)
		})
	}
}
func TestGenerationSettlementTransactionFailureRollsBack(t *testing.T) {
	s, db, in := settlementFixture(t)
	_, e := db.Exec(`CREATE FUNCTION reject_spent() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$ LANGUAGE plpgsql; CREATE TRIGGER reject_spent BEFORE UPDATE OF spent_credits ON projects FOR EACH ROW EXECUTE PROCEDURE reject_spent()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SettleGeneration(in); e == nil {
		t.Fatal("expected transaction error")
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	var st string
	db.QueryRow(`SELECT status FROM shots WHERE id='s'`).Scan(&st)
	if st != "QUEUED" {
		t.Fatal(st)
	}
}
func TestGenerationSettlementRejectsChangedFactsAndStaleAttempt(t *testing.T) {
	s, db, in := settlementFixture(t)
	stale := in
	stale.TaskID = "old-task"
	if _, e := s.SettleGeneration(stale); e == nil {
		t.Fatal("stale task accepted")
	}
	if _, e := s.SettleGeneration(in); e != nil {
		t.Fatal(e)
	}
	in.ChargeMicros++
	if _, e := s.SettleGeneration(in); e == nil {
		t.Fatal("conflicting receipt accepted")
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestGenerationSettlementLegacyDoesNotRecharge(t *testing.T) {
	s, db, in := settlementFixture(t)
	if _, e := db.Exec(`UPDATE shots SET status='GENERATED',video_url='old.mp4',charge_micros=2000000 WHERE id='s'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SettleGeneration(in); e != nil {
		t.Fatal(e)
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	var st string
	if e := db.QueryRow(`SELECT status FROM generation_settlements WHERE upstream_id='task-1'`).Scan(&st); e != nil {
		t.Fatal(e)
	}
	if st != "legacy_unverified" {
		t.Fatal(st)
	}
}
func TestKeyvisualSettlementExactlyThirty(t *testing.T) {
	s, db, _ := settlementFixture(t)
	if _, e := db.Exec(`UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='image-1' WHERE id='p'`); e != nil {
		t.Fatal(e)
	}
	in := GenerationSettlement{Kind: "image", TaskID: "image-1", ProjectID: "p", URL: "http://127.0.0.1/image.png", ChargeMicros: 999999}
	for i := 0; i < 2; i++ {
		if _, e := s.SettleGeneration(in); e != nil {
			t.Fatal(e)
		}
	}
	assertSettlementTotals(t, db, 970, 30, 1)
}
func TestGenerationSettlementZeroChargeStillDeduplicates(t *testing.T) {
	s, db, in := settlementFixture(t)
	in.ChargeMicros = 4999
	for i := 0; i < 2; i++ {
		if _, e := s.SettleGeneration(in); e != nil {
			t.Fatal(e)
		}
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
}

func TestVideoSuccessUsesAtomicSettlement(t *testing.T) {
	s, db, _ := settlementFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(syntheticVideo(t)) }))
	defer srv.Close()
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("PUBLIC_BASE_URL", srv.URL)
	task := &VideoTaskResp{ID: "task-1", Status: "succeeded", URL: srv.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
	s.onShotSucceeded("p", "s", "S01", task)
	s.onShotSucceeded("p", "s", "S01", task)
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestGetVideoRejectsMismatchedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"other-task","status":"succeeded"}`))
	}))
	defer srv.Close()
	c := &LiMengClient{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.GetVideo("task-1"); err == nil {
		t.Fatal("mismatched upstream task accepted")
	}
}
func TestFailedDownloadPreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "existing.mp4")
	if e := os.WriteFile(dst, []byte("previous-video"), 0600); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "999")
		w.Write([]byte("partial"))
	}))
	defer srv.Close()
	c := &LiMengClient{}
	if _, e := c.downloadTo(srv.URL, dir, "existing.mp4"); e == nil {
		t.Fatal("partial download accepted")
	}
	got, e := os.ReadFile(dst)
	if e != nil {
		t.Fatal(e)
	}
	if string(got) != "previous-video" {
		t.Fatalf("old file overwritten: %q", got)
	}
}

func TestReviewCannotBypassUnsettledGeneration(t *testing.T) {
	s, db, _ := settlementFixture(t)
	if e := s.ApproveShot("p", 1); e == nil {
		t.Fatal("queued shot approved before settlement")
	}
	if e := s.UnapproveShot("p", 1); e == nil {
		t.Fatal("queued shot changed to generated")
	}
	var status string
	if e := db.QueryRow(`SELECT status FROM shots WHERE id='s'`).Scan(&status); e != nil {
		t.Fatal(e)
	}
	if status != "QUEUED" {
		t.Fatal(status)
	}
}
