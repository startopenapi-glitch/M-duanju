package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func reviewTask3OldJobsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `DROP INDEX idx_jobs_dedup; ALTER TABLE jobs DROP COLUMN lease_token, DROP COLUMN phase, DROP COLUMN reserved_credits, DROP COLUMN error_code; CREATE UNIQUE INDEX idx_jobs_dedup ON jobs(kind,project_id,COALESCE(shot_id,'')) WHERE status IN ('queued','running')`)
}
func reviewTask3Migrate010(t *testing.T, db *sql.DB) {
	t.Helper()
	b, err := os.ReadFile("../../migrations/010_job_leases.sql")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, string(b))
}

func TestReviewTask3ReconcileConcurrentCrossUserIdentity(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE shots SET upstream_id=NULL; INSERT INTO users(id,email,password_hash,credits) VALUES('u2','review2@example.com','unused',1000); INSERT INTO projects(id,user_id,idea,budget_credits) VALUES('p2','u2','synthetic',1000); INSERT INTO shots(id,project_id,shot_code,seq,status) VALUES('s2','p2','S01',1,'QUEUED'); INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,reserved_credits) VALUES('j1','shot','p','s',1,'reconcile','submitting',275),('j2','shot','p2','s2',1,'reconcile','submitting',275)`)
	// Give both transactions time to complete the non-locking EXISTS before either writes.
	mustExec(t, db, `CREATE FUNCTION review_binding_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.status='reconcile' AND NEW.status='queued' THEN PERFORM pg_sleep(0.5); END IF; RETURN NEW; END $$; CREATE TRIGGER review_binding_delay BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE PROCEDURE review_binding_delay()`)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, item := range [][2]string{{"p", "j1"}, {"p2", "j2"}} {
		wg.Add(1)
		go func(project, id string) {
			defer wg.Done()
			<-start
			errs <- s.ReconcileSubmission(project, id, "same-provider-task")
		}(item[0], item[1])
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			t.Logf("binding rejected: %v", err)
		}
	}
	var bindings int
	if err := db.QueryRow(`SELECT count(*) FROM jobs WHERE upstream_id='same-provider-task'`).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	t.Logf("successful bindings=%d jobs with identical provider ID=%d", successes, bindings)
	if successes != 1 || bindings != 1 {
		t.Fatalf("provider identity must have exactly one owner; successes=%d bindings=%d", successes, bindings)
	}
}

func TestReviewTask3ReconcileRejectsLegacyForeignOwner(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE shots SET upstream_id=NULL; INSERT INTO users(id,email,password_hash,credits) VALUES('u2','legacy2@example.com','unused',1000); INSERT INTO projects(id,user_id,idea,budget_credits) VALUES('p2','u2','synthetic',1000); INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id) VALUES('s2','p2','S01',1,'QUEUED','foreign-paid-task'); INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,reserved_credits) VALUES('j1','shot','p','s',1,'reconcile','submitting',275)`)
	err := s.ReconcileSubmission("p", "j1", "foreign-paid-task")
	var owners int
	if e := db.QueryRow(`SELECT count(DISTINCT p.user_id) FROM shots s JOIN projects p ON p.id=s.project_id WHERE s.upstream_id='foreign-paid-task'`).Scan(&owners); e != nil {
		t.Fatal(e)
	}
	t.Logf("reconcile error=%v distinct owners of same current ID=%d", err, owners)
	if err == nil || owners != 1 {
		t.Fatalf("must reject already-owned legacy provider task: error=%v owners=%d", err, owners)
	}
}

func TestReviewTask3MigrationCompletedLegacyJobReleasesReservation(t *testing.T) {
	s, db, _ := settlementFixture(t)
	reviewTask3OldJobsSchema(t, db)
	mustExec(t, db, `UPDATE shots SET status='APPROVED',duration=5,video_url='old-delivered.mp4',delivered_upstream_id='task-1'; INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url) VALUES('limeng','video','task-1','p','s','u',0,0,'legacy_unverified','old-delivered.mp4'); INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,upstream_id,heartbeat_at) VALUES('old-running','shot','p','s',1,'running','task-1',now())`)
	reviewTask3Migrate010(t, db)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Errorf("unexpected paid POST")
		}
		fmt.Fprint(w, `{"id":"task-1","status":"succeeded","charge_micros":2000000,"duration":5,"resolution":"720P"}`)
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	err := s.ExecuteJob(claimOne(t, s, JobShot))
	var status, url string
	var reserved int
	if e := db.QueryRow(`SELECT j.status,j.reserved_credits,s.video_url FROM jobs j JOIN shots s ON s.id=j.shot_id WHERE j.id='old-running'`).Scan(&status, &reserved, &url); e != nil {
		t.Fatal(e)
	}
	t.Logf("execute error=%v status=%s reserved=%d oldURL=%s GETs=%d", err, status, reserved, url, calls.Load())
	assertSettlementTotals(t, db, 1000, 0, 0)
	if err != nil || status != "done" || reserved != 0 || url != "old-delivered.mp4" {
		t.Fatalf("already-delivered legacy job must complete without retaining reservation")
	}
}

func TestReviewTask3MigrationHonorsActualResolutionReservation(t *testing.T) {
	s, db := queueFixture(t)
	t.Setenv("VIDEO_RESOLUTION", "1080P")
	reviewTask3OldJobsSchema(t, db)
	mustExec(t, db, `UPDATE users SET credits=450 WHERE id='u'; INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,payload) VALUES('old-ready','shot','p','s',1,'queued','{"resolution":"1080P"}'); INSERT INTO projects(id,user_id,idea,budget_credits,keyvisual_url) VALUES('p2','u','synthetic',1000,'synthetic.jpg'); INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES('s2','p2','S01',1,0.5)`)
	reviewTask3Migrate010(t, db)
	var reserved int64
	if e := db.QueryRow(`SELECT reserved_credits FROM jobs WHERE id='old-ready'`).Scan(&reserved); e != nil {
		t.Fatal(e)
	}
	n, err := s.GenerateShots("p2", "u", nil)
	t.Logf("legacy reservation=%d expected=%d second queue count=%d error=%v actual total estimates=%d balance=450", reserved, EstimateShotCost(5, "1080P"), n, err, EstimateShotCost(5, "1080P")+80)
	if reserved != EstimateShotCost(5, "1080P") || err == nil {
		t.Fatalf("migration under-reserves confirmed 1080P job and permits overbooking")
	}
}

func TestReviewTask3BudgetResumeThroughWorker(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE users SET credits=2000 WHERE id='u'`)
	body := syntheticVideo(t)
	var endpoint string
	var creates, queries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
			fmt.Fprint(w, `{"id":"budget-recovery-task"}`)
			return
		}
		if r.URL.Path == "/videos/budget-recovery-task" {
			queries.Add(1)
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
	if _, err := s.GenerateShots("p", "u", nil); err != nil {
		t.Fatal(err)
	}
	j := claimOne(t, s, JobShot)
	if err := s.ExecuteJob(j); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now()`)
	if err := s.ExecuteJob(claimOne(t, s, JobShot)); err == nil {
		t.Fatal("expected budget pause")
	}
	jobs, err := s.ListGenerationJobs("p")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(jobs)
	var fields []map[string]interface{}
	json.Unmarshal(raw, &fields)
	if len(fields) != 1 || fields[0]["requiredCredits"] != float64(1200) || fields[0]["availableCredits"] != float64(1000) {
		t.Fatalf("missing structured settlement difference: %s", raw)
	}
	// 客户侧预算输入已取消；这里直接模拟服务端按本次授权范围补足上限。
	mustExec(t, db, `UPDATE projects SET budget_credits=2000 WHERE id='p'`)
	if err := s.ResumeJob("p", j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecuteJob(claimOne(t, s, JobShot)); err != nil {
		t.Fatal(err)
	}
	var status, phase string
	var reserved, attempts int
	if err := db.QueryRow(`SELECT status,phase,reserved_credits,attempts FROM jobs WHERE id=$1`, j.ID).Scan(&status, &phase, &reserved, &attempts); err != nil {
		t.Fatal(err)
	}
	assertSettlementTotals(t, db, 800, 1200, 1)
	t.Logf("after UpdateProject+ResumeJob+ExecuteJob: status=%s phase=%s reserved=%d attempts=%d POSTs=%d GETs=%d", status, phase, reserved, attempts, creates.Load(), queries.Load())
	if status != "done" || reserved != 0 || attempts != 0 || creates.Load() != 1 || queries.Load() != 1 {
		t.Fatal("budget recovery violated original-task semantics")
	}
}

func TestReviewTask3ConcurrentImageAndShotsCannotStrandPaidImage(t *testing.T) {
	s, db := queueFixture(t)
	body := syntheticImage(t)
	var endpoint string
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
			fmt.Fprint(w, `{"id":"new-image-task"}`)
			return
		}
		if r.URL.Path == "/images/new-image-task" {
			fmt.Fprintf(w, `{"id":"new-image-task","status":"succeeded","url":%q,"charge_micros":300000}`, endpoint+"/asset")
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	endpoint = srv.URL
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("PUBLIC_BASE_URL", srv.URL)
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	if _, err := s.EnqueueJob(JobKeyVisual, "p", "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ExecuteJob(claimOne(t, s, JobKeyVisual)); err != nil {
		t.Fatal(err)
	}
	n, queueErr := s.GenerateShots("p", "u", nil)
	if queueErr != nil {
		t.Logf("safe refusal to start conflicting shot phase: %v", queueErr)
		return
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now() WHERE kind='keyvisual'`)
	err := s.ExecuteJob(claimOne(t, s, JobKeyVisual))
	var status, jobStatus, phase, task string
	var reserved int
	if e := db.QueryRow(`SELECT p.status,j.status,j.phase,p.keyvisual_task_id,j.reserved_credits FROM jobs j JOIN projects p ON p.id=j.project_id WHERE j.kind='keyvisual'`).Scan(&status, &jobStatus, &phase, &task, &reserved); e != nil {
		t.Fatal(e)
	}
	t.Logf("shots queued=%d image error=%v project=%s image job=%s/%s task=%s reserve=%d POSTs=%d", n, err, status, jobStatus, phase, task, reserved, creates.Load())
	if err != nil || jobStatus != "done" || reserved != 0 {
		t.Fatal("accepted shot queue changed project status and stranded already-paid image")
	}
}
