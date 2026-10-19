package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestReviewTask2UniqueKeyAcrossTargets(t *testing.T) {
	s, db, a := settlementFixture(t)
	if _, e := db.Exec("INSERT INTO users(id,email,password_hash,credits) VALUES('u2','u2@example.com','unused',1000); INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('p2','u2','synthetic',1000,'SHOTS_GENERATING'); INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id) VALUES('s2','p2','S01',1,'QUEUED','task-1')"); e != nil {
		t.Fatal(e)
	}
	b := a
	b.ProjectID = "p2"
	b.ShotID = "s2"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, in := range []GenerationSettlement{a, b} {
		wg.Add(1)
		go func(in GenerationSettlement) { defer wg.Done(); <-start; _, e := s.SettleGeneration(in); errs <- e }(in)
	}
	close(start)
	wg.Wait()
	close(errs)
	conflicts := 0
	for e := range errs {
		if e != nil {
			conflicts++
			if e.Error() != "conflicting settlement facts" {
				t.Errorf("unexpected error: %v", e)
			}
		}
	}
	var balances, spent int64
	var receipts, ledgers int
	if e := db.QueryRow("SELECT (SELECT sum(credits) FROM users),(SELECT sum(spent_credits) FROM projects),(SELECT count(*) FROM generation_settlements),(SELECT count(*) FROM credit_transactions WHERE kind='spend')").Scan(&balances, &spent, &receipts, &ledgers); e != nil {
		t.Fatal(e)
	}
	t.Logf("conflicts=%d combined_balance=%d spent=%d receipts=%d ledgers=%d", conflicts, balances, spent, receipts, ledgers)
	if conflicts != 1 || balances != 1800 || spent != 200 || receipts != 1 || ledgers != 1 {
		t.Fatal("unique-key collision did not reject exactly one target atomically")
	}
}

func TestReviewTask2LegacyAfterRegenerateRejected(t *testing.T) {
	s, db, in := settlementFixture(t)
	if _, e := db.Exec("UPDATE projects SET keyvisual_url='synthetic.jpg',budget_credits=0 WHERE id='p'; UPDATE shots SET status='GENERATED',video_url='old.mp4',charge_micros=2000000 WHERE id='s'"); e != nil {
		t.Fatal(e)
	}
	if e := s.RegenerateShot("p", 1, false); e == nil {
		t.Fatal("expected budget preflight rejection")
	}
	var st, task, artifact string
	if e := db.QueryRow("SELECT status,upstream_id,COALESCE(video_url,'') FROM shots WHERE id='s'").Scan(&st, &task, &artifact); e != nil {
		t.Fatal(e)
	}
	t.Logf("after rejected regeneration: status=%s task=%s artifact=%q", st, task, artifact)
	if _, e := db.Exec("UPDATE projects SET budget_credits=1000 WHERE id='p'"); e != nil {
		t.Fatal(e)
	}
	settled, e := s.SettleGeneration(in)
	if e != nil {
		t.Fatal(e)
	}
	var bal, spent int64
	var ledger int
	if e := db.QueryRow("SELECT (SELECT credits FROM users WHERE id='u'),(SELECT spent_credits FROM projects WHERE id='p'),(SELECT count(*) FROM credit_transactions WHERE kind='spend')").Scan(&bal, &spent, &ledger); e != nil {
		t.Fatal(e)
	}
	t.Logf("old-task replay: settled=%v balance=%d spent=%d ledgers=%d", settled, bal, spent, ledger)
	if artifact != "old.mp4" || settled || bal != 1000 || spent != 0 || ledger != 0 {
		t.Fatal("historical artifact/protection lost across rejected manual regeneration")
	}
}

func TestReviewTask2PendingRejectsChangedResult(t *testing.T) {
	s, db, in := settlementFixture(t)
	if _, e := db.Exec("UPDATE users SET credits=100 WHERE id='u'"); e != nil {
		t.Fatal(e)
	}
	_, e := s.SettleGeneration(in)
	var pending *SettlementPendingError
	if !errors.As(e, &pending) {
		t.Fatalf("want pending: %v", e)
	}
	if _, e := db.Exec("UPDATE users SET credits=1000 WHERE id='u'"); e != nil {
		t.Fatal(e)
	}
	changed := in
	changed.URL = "http://127.0.0.1/different.mp4"
	changed.Duration = 9
	changed.Width = 1920
	changed.Height = 1088
	settled, e := s.SettleGeneration(changed)
	var receiptURL, shotURL string
	var duration float64
	if e := db.QueryRow("SELECT r.result_url,COALESCE(s.video_url,''),COALESCE(s.duration_actual,0) FROM generation_settlements r JOIN shots s ON s.id=r.shot_id WHERE r.upstream_id='task-1'").Scan(&receiptURL, &shotURL, &duration); e != nil {
		t.Fatal(e)
	}
	t.Logf("settled=%v err=%v receipt_url=%s delivered_url=%s duration=%v", settled, e, receiptURL, shotURL, duration)
	if e == nil {
		t.Fatal("changed result facts accepted for an existing pending receipt")
	}
}

func TestReviewTask2WriteFailureKeepsPendingFacts(t *testing.T) {
	s, db, in := settlementFixture(t)
	if _, e := db.Exec("CREATE FUNCTION review_reject_spent() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic write failure'; END $$ LANGUAGE plpgsql; CREATE TRIGGER review_reject_spent BEFORE UPDATE OF spent_credits ON projects FOR EACH ROW EXECUTE PROCEDURE review_reject_spent()"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SettleGeneration(in); e == nil {
		t.Fatal("expected write error")
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	var receipts int
	if e := db.QueryRow("SELECT count(*) FROM generation_settlements WHERE upstream_id='task-1' AND status='pending'").Scan(&receipts); e != nil {
		t.Fatal(e)
	}
	t.Logf("pending receipts after transient write failure=%d", receipts)
	if receipts != 1 {
		t.Fatal("completed provider facts and pending write failure were not persisted")
	}
}

func TestReviewTask2ZeroRowDeliveryMustRollback(t *testing.T) {
	s, db, in := settlementFixture(t)
	if _, e := db.Exec("CREATE FUNCTION review_skip_delivery() RETURNS trigger AS $$ BEGIN RETURN NULL; END $$ LANGUAGE plpgsql; CREATE TRIGGER review_skip_delivery BEFORE UPDATE OF video_url ON shots FOR EACH ROW EXECUTE PROCEDURE review_skip_delivery()"); e != nil {
		t.Fatal(e)
	}
	settled, e := s.SettleGeneration(in)
	var balance, spent int64
	var st, receipt string
	var ledgers int
	if q := db.QueryRow("SELECT (SELECT credits FROM users WHERE id='u'),(SELECT spent_credits FROM projects WHERE id='p'),(SELECT status FROM shots WHERE id='s'),(SELECT status FROM generation_settlements WHERE upstream_id='task-1'),(SELECT count(*) FROM credit_transactions WHERE kind='spend')").Scan(&balance, &spent, &st, &receipt, &ledgers); q != nil {
		t.Fatal(q)
	}
	t.Logf("settled=%v err=%v balance=%d spent=%d shot=%s receipt=%s ledger=%d", settled, e, balance, spent, st, receipt, ledgers)
	if e == nil || settled || balance != 1000 || spent != 0 || ledgers != 0 {
		t.Fatal("zero-row delivery committed charges and settled receipt")
	}
}

func TestReviewTask2RejectedReplayPreservesDeliveredMedia(t *testing.T) {
	s, db, _ := settlementFixture(t)
	var body atomic.Value
	body.Store(syntheticVideo(t))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body.Load().([]byte)) }))
	defer srv.Close()
	root := filepath.Join(t.TempDir(), "storage")
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("PUBLIC_BASE_URL", srv.URL)
	t.Setenv("FFMPEG_PATH", "/bin/false")
	task := &VideoTaskResp{ID: "task-1", Status: "succeeded", URL: srv.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
	if e := s.onShotSucceeded("p", "s", "S01", task); e != nil {
		t.Fatal(e)
	}
	var servedURL string
	if e := db.QueryRow(`SELECT video_url FROM shots WHERE id='s'`).Scan(&servedURL); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(root, strings.TrimPrefix(servedURL, srv.URL+"/files/"))
	body.Store([]byte{})
	task.ChargeMicros++
	e := s.onShotSucceeded("p", "s", "S01", task)
	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
	t.Logf("conflicting replay err=%v served_file_bytes=%d", e, len(got))
	if e == nil || string(got) != string(syntheticVideo(t)) {
		t.Fatal("rejected callback replaced previously delivered media before conflict validation")
	}
}

func TestReviewTask2ImageRejectsMismatchedID(t *testing.T) {
	s, db, _ := settlementFixture(t)
	if _, e := db.Exec("UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='image-1' WHERE id='p'"); e != nil {
		t.Fatal(e)
	}
	var endpoint string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/image-1" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"other-image","status":"succeeded","url":%q,"charge_micros":123}`, endpoint+"/asset")
			return
		}
		fmt.Fprint(w, "synthetic-image")
	}))
	defer srv.Close()
	endpoint = srv.URL
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("PUBLIC_BASE_URL", srv.URL)
	t.Setenv("FFMPEG_PATH", "/bin/false")
	e := s.waitKeyVisual("p", "u", "image-1")
	var balance, spent int64
	var task string
	if q := db.QueryRow("SELECT (SELECT credits FROM users WHERE id='u'),spent_credits,COALESCE(keyvisual_task_id,'') FROM projects WHERE id='p'").Scan(&balance, &spent, &task); q != nil {
		t.Fatal(q)
	}
	t.Logf("mismatched image response err=%v balance=%d spent=%d current_task=%q", e, balance, spent, task)
	if e == nil || balance != 1000 || spent != 0 || task != "image-1" {
		t.Fatal("unrelated image result settled against the requested image task")
	}
}
