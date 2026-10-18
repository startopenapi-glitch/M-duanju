package service

import (
	"database/sql"
	"drama-platform/internal/model"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueTransientFalsePositives(t *testing.T) {
	for _, msg := range []string{"room unavailable", "error 5020", "invalid GPU config", "CUDA invalid argument"} {
		if IsTransientUpstream(msg) {
			t.Errorf("false transient: %s", msg)
		}
	}
}
func TestQueueFixedRetryAllowance(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE shots SET status='APPROVED',video_url='old.mp4' WHERE id='s'`)
	id, e := s.EnqueueJob(JobAssemble, "p", "", 0, "")
	if e != nil {
		t.Fatal(e)
	}
	for n := 1; n <= 7; n++ {
		db.Exec(`UPDATE jobs SET status='queued',run_after=now() WHERE id=$1`, id)
		js, e := s.ClaimJobs(JobAssemble, 1)
		if e != nil || len(js) != 1 {
			t.Fatalf("claim %v %v", js, e)
		}
		retry, err := s.FailJob(id, js[0].LeaseToken, fmt.Errorf("HTTP 503"), true)
		if err != nil {
			t.Fatal(err)
		}
		var max int
		var delay float64
		if e = db.QueryRow(`SELECT max_attempts,extract(epoch from run_after-now()) FROM jobs WHERE id=$1`, id).Scan(&max, &delay); e != nil {
			t.Fatal(e)
		}
		if max != 2 {
			t.Errorf("baseline changed to %d at failure %d", max, n)
		}
		if delay > float64((10 * time.Minute).Seconds()) {
			t.Errorf("uncapped delay %f", delay)
		}
		if n >= 6 && retry {
			t.Errorf("retried after fixed allowance: %d", n)
		}
	}
}
func TestQueueAtomicProjectEdit(t *testing.T) {
	s, db, _ := settlementFixture(t)
	// 预算由服务端按授权范围维护：客户端传 budgetYuan 也不得改写。
	e := s.UpdateProject("p", &model.UpdateProjectReq{Title: "changed", BudgetYuan: 20, StyleConfig: json.RawMessage(`{"x":1}`)})
	if e != nil {
		t.Fatal(e)
	}
	var title, style string
	var budget int64
	db.QueryRow(`SELECT title,budget_credits,style_config::text FROM projects WHERE id='p'`).Scan(&title, &budget, &style)
	if title != "changed" || budget != 1000 || style != "{\"x\": 1}" {
		t.Fatalf("partial update %s %d %s", title, budget, style)
	}
	e = s.UpdateProject("p", &model.UpdateProjectReq{Title: "must rollback", BudgetYuan: 30, StyleConfig: json.RawMessage(`invalid`)})
	if e == nil {
		t.Fatal("invalid style accepted")
	}
	db.QueryRow(`SELECT title,budget_credits FROM projects WHERE id='p'`).Scan(&title, &budget)
	if title != "changed" || budget != 1000 {
		t.Fatal("partial edit persisted")
	}
}

func queueFixture(t *testing.T) (*Service, *sql.DB) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE shots SET status='PENDING',upstream_id=NULL,duration=5 WHERE id='s';UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='p'`)
	return s, db
}
func claimOne(t *testing.T, s *Service, kind string) *Job {
	t.Helper()
	js, e := s.ClaimJobs(kind, 1)
	if e != nil || len(js) != 1 {
		t.Fatalf("claim %d %v", len(js), e)
	}
	return js[0]
}
func TestQueueReservationsAcrossProjects(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE users SET credits=300 WHERE id='u';INSERT INTO projects(id,user_id,idea,budget_credits,keyvisual_url) VALUES('p2','u','synthetic',1000,'x');INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES('s2','p2','S01',1,5)`)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, p := range []string{"p", "p2"} {
		go func(p string) { <-start; _, e := s.GenerateShots(p, "u", nil); errs <- e }(p)
	}
	close(start)
	ok := 0
	for i := 0; i < 2; i++ {
		if <-errs == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("accepted %d batches with balance for one", ok)
	}
	var reserved, jobs int
	if e := db.QueryRow(`SELECT COALESCE(sum(reserved_credits),0),count(*) FROM jobs`).Scan(&reserved, &jobs); e != nil {
		t.Fatal(e)
	}
	if reserved != 200 || jobs != 1 {
		t.Fatalf("reservation %d jobs %d", reserved, jobs)
	}
}
func TestQueueLeaseFencesSettlementAndMutation(t *testing.T) {
	s, db := queueFixture(t)
	if _, e := s.GenerateShots("p", "u", nil); e != nil {
		t.Fatal(e)
	}
	a := claimOne(t, s, JobShot)
	mustExec(t, db, `UPDATE jobs SET upstream_id='known',phase='polling',heartbeat_at=now()-interval '1 hour';UPDATE shots SET upstream_id='known'`)
	if n := s.ReclaimStaleJobs(time.Minute); n != 1 {
		t.Fatal(n)
	}
	b := claimOne(t, s, JobShot)
	facts := GenerationSettlement{Kind: "video", TaskID: "known", ProjectID: "p", ShotID: "s", URL: "new.mp4", ChargeMicros: 2000000}
	if _, e := s.withJob(a).SettleGeneration(facts); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("stale settlement accepted: %v", e)
	}
	if e := s.RenewJob(a.ID, a.LeaseToken); !errors.Is(e, ErrLeaseLost) {
		t.Fatal(e)
	}
	if e := s.FinishJob(a.ID, a.LeaseToken, "known"); !errors.Is(e, ErrLeaseLost) {
		t.Fatal(e)
	}
	if _, e := s.FailJob(a.ID, a.LeaseToken, errors.New("timeout"), true); !errors.Is(e, ErrLeaseLost) {
		t.Fatal(e)
	}
	if ok, e := s.withJob(b).SettleGeneration(facts); e != nil || !ok {
		t.Fatalf("valid settlement %v %v", ok, e)
	}
	var reserved int
	db.QueryRow(`SELECT reserved_credits FROM jobs WHERE id=$1`, b.ID).Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("reservation not released %d", reserved)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestQueueUnknownCreateNeverRetries(t *testing.T) {
	s, db := queueFixture(t)
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		http.Error(w, "unknown after upstream accept", 502)
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	if _, e := s.GenerateShots("p", "u", nil); e != nil {
		t.Fatal(e)
	}
	j := claimOne(t, s, JobShot)
	if e := s.ExecuteJob(j); !errors.Is(e, ErrSubmissionUnknown) {
		t.Fatalf("unknown %v", e)
	}
	s.RecoverStuckJobs()
	s.PollShots("p")
	if e := s.RegenerateShot("p", 1, false); e == nil {
		t.Fatal("manual retry bypassed unknown")
	}
	var status, phase string
	var reserved int
	if e := db.QueryRow(`SELECT status,phase,reserved_credits FROM jobs WHERE id=$1`, j.ID).Scan(&status, &phase, &reserved); e != nil {
		t.Fatal(e)
	}
	if status != "reconcile" || phase != "submitting" || reserved != 200 || creates.Load() != 1 {
		t.Fatalf("%s %s %d creates=%d", status, phase, reserved, creates.Load())
	}
}
func TestQueueCrashAfterIntentRequiresReconciliation(t *testing.T) {
	s, db := queueFixture(t)
	s.GenerateShots("p", "u", nil)
	j := claimOne(t, s, JobShot)
	mustExec(t, db, `UPDATE jobs SET phase='submitting',heartbeat_at=now()-interval '1 hour'`)
	s.ReclaimStaleJobs(time.Minute)
	var status string
	db.QueryRow(`SELECT status FROM jobs WHERE id=$1`, j.ID).Scan(&status)
	if status != "reconcile" {
		t.Fatal(status)
	}
	if js, e := s.ClaimJobs(JobShot, 1); e != nil || len(js) != 0 {
		t.Fatalf("unsafe recovery %v %v", js, e)
	}
}
func TestQueuePollsDoNotConsumeAttempts(t *testing.T) {
	s, db := queueFixture(t)
	var creates, queries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
			fmt.Fprint(w, `{"id":"same-task"}`)
		} else {
			queries.Add(1)
			fmt.Fprint(w, `{"status":"processing"}`)
		}
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	s.GenerateShots("p", "u", nil)
	for i := 0; i < 8; i++ {
		mustExec(t, db, `UPDATE jobs SET run_after=now()`)
		if e := s.ExecuteJob(claimOne(t, s, JobShot)); e != nil {
			t.Fatal(e)
		}
	}
	var attempts int
	db.QueryRow(`SELECT attempts FROM jobs`).Scan(&attempts)
	if attempts != 0 || creates.Load() != 1 || queries.Load() != 7 {
		t.Fatalf("attempts=%d POST=%d GET=%d", attempts, creates.Load(), queries.Load())
	}
}
func TestQueueFailureWriteErrorPropagates(t *testing.T) {
	s, db := queueFixture(t)
	s.GenerateShots("p", "u", nil)
	j := claimOne(t, s, JobShot)
	mustExec(t, db, `CREATE FUNCTION reject_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.last_error IS NOT NULL THEN RAISE EXCEPTION 'synthetic failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_failure BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE PROCEDURE reject_failure()`)
	if retry, e := s.FailJob(j.ID, j.LeaseToken, errors.New("timeout"), true); e == nil || retry {
		t.Fatalf("false persisted retry %v %v", retry, e)
	}
}

func TestQueueSlowWorkRenewsIndependentLease(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET status='DRAFT';INSERT INTO projects(id,user_id,idea,budget_credits,keyvisual_url) VALUES('p2','u','synthetic',1000,'image.jpg');UPDATE shots SET project_id='p2' WHERE id='s'`)
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images" {
			close(entered)
			<-release
			fmt.Fprint(w, `{"id":"image-slow"}`)
		} else {
			fmt.Fprint(w, `{"id":"video-fast"}`)
		}
	}))
	defer srv.Close()

	t.Setenv("LIMENG_BASE_URL", srv.URL)
	if _, e := s.EnqueueJob(JobKeyVisual, "p", "", 0, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GenerateShots("p2", "u", nil); e != nil {
		t.Fatal(e)
	}
	image := claimOne(t, s, JobKeyVisual)
	done := make(chan error, 1)
	go func() { done <- s.ExecuteJob(image) }()
	defer func() {
		close(release)
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	<-entered
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e != nil {
		t.Fatal(e)
	}
	// Read after two independent heartbeat intervals while the image POST is blocked.
	time.Sleep(2200 * time.Millisecond)
	if n := s.ReclaimStaleJobs(2 * time.Second); n != 0 {
		t.Fatalf("reclaimed live slow task: %d", n)
	}
	var task string
	if e := db.QueryRow(`SELECT upstream_id FROM shots WHERE id='s'`).Scan(&task); e != nil || task != "video-fast" {
		t.Fatalf("other work blocked: %s %v", task, e)
	}
}
func TestQueueAssembleStaleCannotPublish(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE shots SET status='APPROVED',video_url='old.mp4';UPDATE projects SET final_url='old-final.mp4'`)
	if _, e := s.EnqueueJob(JobAssemble, "p", "", 0, ""); e != nil {
		t.Fatal(e)
	}
	a := claimOne(t, s, JobAssemble)
	mustExec(t, db, `UPDATE jobs SET heartbeat_at=now()-interval '1 hour'`)
	s.ReclaimStaleJobs(time.Minute)
	claimOne(t, s, JobAssemble)
	if e := s.withJob(a).AssembleFinal("p"); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("stale assemble started: %v", e)
	}
	var status, url string
	db.QueryRow(`SELECT status,final_url FROM projects WHERE id='p'`).Scan(&status, &url)
	if url != "old-final.mp4" || status == "ASSEMBLING" {
		t.Fatalf("stale publication changed %s %s", status, url)
	}
}

func TestQueueDownloadRetryUsesOriginalIDAndReservationOnce(t *testing.T) {
	s, db := queueFixture(t)
	body := syntheticVideo(t)
	var creates, gets, downloads atomic.Int32
	var endpoint string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
			fmt.Fprint(w, `{"id":"paid-once"}`)
			return
		}
		if r.URL.Path == "/videos/paid-once" {
			gets.Add(1)
			fmt.Fprintf(w, `{"status":"succeeded","url":%q,"charge_micros":2000000,"duration":5,"resolution":"720P"}`, endpoint+"/asset")
			return
		}
		if downloads.Add(1) == 1 {
			http.Error(w, "temporary download failure", 503)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	endpoint = srv.URL
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("PUBLIC_BASE_URL", srv.URL)
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("FFMPEG_PATH", "/bin/false")
	s.GenerateShots("p", "u", nil)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now()`)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e == nil {
		t.Fatal("download should fail")
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now()`)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e != nil {
		t.Fatal(e)
	}
	s.PollShots("p")
	s.GenerateShots("p", "u", nil)
	var status string
	var reserved int
	db.QueryRow(`SELECT status,reserved_credits FROM jobs`).Scan(&status, &reserved)
	if status != "done" || reserved != 0 || creates.Load() != 1 || gets.Load() != 2 {
		t.Fatalf("%s reserved=%d POST=%d GET=%d", status, reserved, creates.Load(), gets.Load())
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestQueueBindWriteFailureKeepsIntent(t *testing.T) {
	s, db := queueFixture(t)
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		fmt.Fprint(w, `{"id":"accepted-but-save-failed"}`)
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	s.GenerateShots("p", "u", nil)
	mustExec(t, db, `CREATE FUNCTION reject_bind() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.upstream_id IS NOT NULL THEN RAISE EXCEPTION 'synthetic bind failure'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_bind BEFORE UPDATE ON shots FOR EACH ROW EXECUTE PROCEDURE reject_bind()`)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); !errors.Is(e, ErrSubmissionUnknown) {
		t.Fatalf("bind error %v", e)
	}
	mustExec(t, db, `DROP TRIGGER reject_bind ON shots`)
	s.RecoverStuckJobs()
	s.PollShots("p")
	if e := s.RegenerateShot("p", 1, false); e == nil {
		t.Fatal("accepted a second paid request")
	}
	var phase, status string
	db.QueryRow(`SELECT phase,status FROM jobs`).Scan(&phase, &status)
	if phase != "submitting" || status != "reconcile" || creates.Load() != 1 {
		t.Fatalf("%s %s POST=%d", phase, status, creates.Load())
	}
}
func TestQueueAssemblyLosesLeaseDuringWorkPreservesOldFile(t *testing.T) {
	s, db := queueFixture(t)
	root := filepath.Join(t.TempDir(), "storage")
	os.MkdirAll(filepath.Join(root, "exports"), 0755)
	old := filepath.Join(root, "exports", "p-final.mp4")
	os.WriteFile(old, []byte("old-final"), 0600)
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.Write([]byte("synthetic")) }))
	defer srv.Close()
	mustExec(t, db, `UPDATE shots SET status='APPROVED';UPDATE projects SET final_url='old-final.mp4'`)
	if _, e := db.Exec(`UPDATE shots SET video_url=$1`, srv.URL); e != nil {
		t.Fatal(e)
	}
	fake := filepath.Join(t.TempDir(), "ffmpeg")
	os.WriteFile(fake, []byte("#!/bin/sh\nfor last; do :; done\nif [ \"$last\" != \"-\" ]; then printf new-final > \"$last\"; fi\n"), 0700)
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("FFMPEG_PATH", fake)
	if _, e := s.EnqueueJob(JobAssemble, "p", "", 0, ""); e != nil {
		t.Fatal(e)
	}
	j := claimOne(t, s, JobAssemble)
	done := make(chan error, 1)
	go func() { done <- s.withJob(j).AssembleFinal("p") }()
	<-entered
	mustExec(t, db, `UPDATE jobs SET heartbeat_at=now()-interval '1 hour'`)
	s.ReclaimStaleJobs(time.Minute)
	claimOne(t, s, JobAssemble)
	close(release)
	if e := <-done; !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("stale work not rejected: %v", e)
	}
	bytes, e := os.ReadFile(old)
	if e != nil || string(bytes) != "old-final" {
		t.Fatalf("old output changed %s %v", bytes, e)
	}
	var final string
	db.QueryRow(`SELECT final_url FROM projects WHERE id='p'`).Scan(&final)
	if final != "old-final.mp4" {
		t.Fatal(final)
	}
}
func TestQueueBudgetPendingDoesNotSpendFailureRetries(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE users SET credits=2000 WHERE id='u'`)
	body := syntheticVideo(t)
	var endpoint string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fmt.Fprint(w, `{"id":"budget-task"}`)
			return
		}
		if r.URL.Path == "/videos/budget-task" {
			fmt.Fprintf(w, `{"status":"succeeded","url":%q,"charge_micros":12000000,"duration":5,"resolution":"720P"}`, endpoint+"/asset")
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	endpoint = srv.URL
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("PUBLIC_BASE_URL", srv.URL)
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("FFMPEG_PATH", "/bin/false")
	s.GenerateShots("p", "u", nil)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now()`)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e == nil {
		t.Fatal("budget should park")
	}
	var status, code string
	var attempts, reserved int
	db.QueryRow(`SELECT status,error_code,attempts,reserved_credits FROM jobs`).Scan(&status, &code, &attempts, &reserved)
	if status != "reconcile" || code != "settlement_budget" || attempts != 0 || reserved != 200 {
		t.Fatalf("%s %s attempts=%d reserve=%d", status, code, attempts, reserved)
	}
}

func TestQueueResumeUsesSameIDAndRejectsUnknown(t *testing.T) {
	s, db := queueFixture(t)
	s.GenerateShots("p", "u", nil)
	j := claimOne(t, s, JobShot)
	mustExec(t, db, `UPDATE jobs SET phase='submitting',status='reconcile',error_code='submission_unknown'`)
	if e := s.ResumeJob("p", j.ID); !errors.Is(e, ErrSubmissionUnknown) {
		t.Fatalf("unknown resumed %v", e)
	}
	if e := s.ReconcileSubmission("p", j.ID, "verified-task"); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE jobs SET status='reconcile',phase='polling',error_code='settlement_budget',attempts=3`)
	if e := s.ResumeJob("p", j.ID); e != nil {
		t.Fatal(e)
	}
	recovered := claimOne(t, s, JobShot)
	if recovered.UpstreamID != "verified-task" || recovered.Attempts != 3 || recovered.ReservedCredits != 200 {
		t.Fatalf("identity/counters changed %+v", recovered)
	}
	if e := s.ResumeJob("other-project", j.ID); e == nil {
		t.Fatal("cross-project resume accepted")
	}
	jobs, e := s.ListGenerationJobs("p")
	if e != nil || len(jobs) != 1 {
		t.Fatalf("status API %v %v", jobs, e)
	}
}
func TestQueueProviderFailureReleasesReservation(t *testing.T) {
	s, db := queueFixture(t)
	s.GenerateShots("p", "u", nil)
	mustExec(t, db, `UPDATE jobs SET phase='polling',upstream_id='failed-task';UPDATE shots SET upstream_id='failed-task'`)
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
		}
		fmt.Fprint(w, `{"status":"failed","error":"invalid content"}`)
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	_ = s.ExecuteJob(claimOne(t, s, JobShot))
	var status string
	var reserved int
	if e := db.QueryRow(`SELECT status,reserved_credits FROM jobs`).Scan(&status, &reserved); e != nil {
		t.Fatal(e)
	}
	if status != "failed" || reserved != 0 || creates.Load() != 0 {
		t.Fatalf("terminal status=%s reservation=%d creates=%d", status, reserved, creates.Load())
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
}
func TestQueuePreSubmissionTerminalAllowsManualRetry(t *testing.T) {
	s, db := queueFixture(t)
	s.GenerateShots("p", "u", nil)
	j := claimOne(t, s, JobShot)
	if retry, e := s.FailJob(j.ID, j.LeaseToken, errors.New("invalid local input"), false); e != nil || retry {
		t.Fatalf("%v %v", retry, e)
	}
	s.PollAllGenerating()
	var status string
	if e := db.QueryRow(`SELECT status FROM shots WHERE id='s'`).Scan(&status); e != nil {
		t.Fatal(e)
	}
	if status != "FAILED" {
		t.Fatalf("terminal pre-submit shot %s", status)
	}
	if e := s.RegenerateShot("p", 1, false); e != nil {
		t.Fatalf("safe retry blocked: %v", e)
	}
}
func TestQueueSettlementFailureRetainsReservation(t *testing.T) {
	s, db := queueFixture(t)
	s.GenerateShots("p", "u", nil)
	mustExec(t, db, `UPDATE jobs SET phase='polling',upstream_id='task';UPDATE shots SET upstream_id='task'; CREATE FUNCTION reject_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='GENERATED' THEN RAISE EXCEPTION 'synthetic delivery failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_delivery BEFORE UPDATE ON shots FOR EACH ROW EXECUTE PROCEDURE reject_delivery()`)
	j := claimOne(t, s, JobShot)
	in := GenerationSettlement{Kind: "video", TaskID: "task", ProjectID: "p", ShotID: "s", URL: "final.mp4", ChargeMicros: 2000000}
	if _, e := s.withJob(j).SettleGeneration(in); e == nil {
		t.Fatal("expected injected failure")
	}
	var reserved int
	db.QueryRow(`SELECT reserved_credits FROM jobs`).Scan(&reserved)
	if reserved != 200 {
		t.Fatalf("released before successful settlement %d", reserved)
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	mustExec(t, db, `DROP TRIGGER reject_delivery ON shots`)
	if ok, e := s.withJob(j).SettleGeneration(in); e != nil || !ok {
		t.Fatalf("recover %v %v", ok, e)
	}
	db.QueryRow(`SELECT reserved_credits FROM jobs`).Scan(&reserved)
	if reserved != 0 {
		t.Fatal(reserved)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
