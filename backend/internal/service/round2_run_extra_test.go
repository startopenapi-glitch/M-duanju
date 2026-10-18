package service

// Independent round-two review probes. Local mocks and guarded test DB only.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRound2CancelledSubmittedJobRemainsClaimable(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	_, creates := providerSubmitCounter(t)
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	j := claimOne(t, svc, JobKeyVisual)
	if err := svc.ExecuteJob(j); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 {
		t.Fatalf("setup expected exactly one submission, got %d", creates.Load())
	}
	if err := svc.CancelRun("review-u", "review-p", oldRun); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE jobs SET run_after=now() WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err := svc.ClaimJobs(JobKeyVisual, 10)
	if err != nil {
		t.Fatal(err)
	}
	var status, phase, upstream string
	var reserved int64
	if err := db.QueryRow(`SELECT status,phase,COALESCE(upstream_id,''),reserved_credits FROM jobs WHERE id=$1`, j.ID).Scan(&status, &phase, &upstream, &reserved); err != nil {
		t.Fatal(err)
	}
	t.Logf("cancel-after-submit: creates=%d job=%s/%s upstream=%s reserved=%d claims=%d", creates.Load(), status, phase, upstream, reserved, len(jobs))
	if len(jobs) != 1 {
		t.Errorf("cancelled run's already submitted task must remain claimable for polling and settlement; got %d", len(jobs))
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "round2-after-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	jobs, err = svc.ClaimJobs(JobKeyVisual, 10)
	if err != nil {
		t.Fatal(err)
	}
	var nextStatus string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id=$1`, start.Run.ID).Scan(&nextStatus); err != nil {
		t.Fatal(err)
	}
	t.Logf("new authorized run=%s, old polling claims=%d, total provider submissions=%d", nextStatus, len(jobs), creates.Load())
}

func TestRound2CancelUnsubmittedKeyvisualCanRestart(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelRun("review-u", "review-p", oldRun); err != nil {
		t.Fatal(err)
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "round2-restart-ready"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		t.Logf("advance[%d]: %v", i, svc.AdvanceRun(context.Background(), start.Run.ID))
	}
	var projectStatus, runStatus, reason string
	if err := db.QueryRow(`SELECT p.status,r.status,r.waiting_reason FROM projects p JOIN generation_runs r ON r.project_id=p.id WHERE r.id=$1`, start.Run.ID).Scan(&projectStatus, &runStatus, &reason); err != nil {
		t.Fatal(err)
	}
	n := countRows(t, db, `SELECT count(*) FROM jobs WHERE run_id=$1`, start.Run.ID)
	t.Logf("restart after ready cancellation: project=%s run=%s/%s new-jobs=%d", projectStatus, runStatus, reason, n)
	if n != 1 {
		t.Fatalf("confirmed unsubmitted cancellation must allow new authorized keyvisual job; got %d", n)
	}
}

func TestRound2UnsupportedReviseMustRejectBeforeRecharge(t *testing.T) {
	svc, db, oldRun := reviewOrchestratorFixture(t, "COMPLETED")
	if _, err := db.Exec(`UPDATE generation_runs SET status='completed' WHERE id=$1`, oldRun); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE users SET credits=0 WHERE id='review-u'; UPDATE projects SET final_url='old-final.mp4',keyvisual_url='old.jpg' WHERE id='review-p'`)
	var budget int64
	if err := db.QueryRow(`SELECT budget_credits FROM projects WHERE id='review-p'`).Scan(&budget); err != nil {
		t.Fatal(err)
	}
	// Explicit unsupported scope, not a legal revision that is now implemented.
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "round2-revise-empty-wallet", Mode: "revise", Scope: json.RawMessage(`{"instruction":"change ending","replaceCast":true}`)})
	if err == nil || err.Error() != "修改范围无效" || start != nil {
		t.Fatalf("invalid scope was not rejected deterministically: %#v %v", start, err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE project_id='review-p'`); n != 1 {
		t.Fatal("invalid scope left rechargeable run", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM recharge_orders`); n != 0 {
		t.Fatal("unexpected orders", n)
	}
	var after int64
	if err := db.QueryRow(`SELECT budget_credits FROM projects WHERE id='review-p'`).Scan(&after); err != nil || after != budget {
		t.Fatal("changed budget", after, err)
	}
}

func TestRound2ChangedProjectOwnerBlocksProviderSubmission(t *testing.T) {
	svc, db, runID := reviewOrchestratorFixture(t, "SCRIPT_REVIEW")
	_, creates := providerSubmitCounter(t)
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	j := claimOne(t, svc, JobKeyVisual)
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES('round2-new-owner','round2@example.invalid','unused',10000,'active'); UPDATE projects SET user_id='round2-new-owner' WHERE id='review-p'`)
	err := svc.ExecuteJob(j)
	var jobStatus, phase string
	if qerr := db.QueryRow(`SELECT status,phase FROM jobs WHERE id=$1`, j.ID).Scan(&jobStatus, &phase); qerr != nil {
		t.Fatal(qerr)
	}
	t.Logf("owner changed before submit: execute=%v creates=%d job=%s/%s", err, creates.Load(), jobStatus, phase)
	if creates.Load() != 0 {
		t.Fatalf("old owner's run still submitted a paid task after project owner changed")
	}
}

func TestRound2CancelledAssemblyCannotFailSuccessorRun(t *testing.T) {
	ff := testEncoder(t)
	t.Setenv("FFMPEG_PATH", ff)
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(ff), "ffprobe"))
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "")
	payload := syntheticVideo(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		w.Write(payload)
	}))
	defer srv.Close()
	defer unblock()
	svc, db, oldRun := reviewOrchestratorFixture(t, "SHOTS_REVIEW")
	mustExec(t, db, `UPDATE projects SET keyvisual_url='synthetic.jpg' WHERE id='review-p'`)
	if _, err := db.Exec(`INSERT INTO shots(id,project_id,shot_code,seq,status,video_url,duration)
		VALUES('round2-assembly-shot','review-p','S01',1,'APPROVED',$1,5)`, srv.URL+"/shot.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), oldRun); err != nil {
		t.Fatal(err)
	}
	j := claimOne(t, svc, JobAssemble)
	done := make(chan error, 1)
	go func() { done <- svc.ExecuteJob(j) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("assembly ended before barrier: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("assembly download barrier not reached")
	}
	if err := svc.CancelRun("review-u", "review-p", oldRun); err != nil {
		t.Fatal(err)
	}
	start, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "round2-successor-during-assembly"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("assembly did not finish after release")
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	var oldStatus, nextStatus, reason, finalRun, projectStatus string
	if err := db.QueryRow(`SELECT a.status,b.status,b.waiting_reason,COALESCE(p.final_run_id,''),p.status
		FROM generation_runs a JOIN generation_runs b ON b.project_id=a.project_id JOIN projects p ON p.id=a.project_id
		WHERE a.id=$1 AND b.id=$2`, oldRun, start.Run.ID).Scan(&oldStatus, &nextStatus, &reason, &finalRun, &projectStatus); err != nil {
		t.Fatal(err)
	}
	t.Logf("late assembly: old=%s next=%s/%s project=%s finalRun=%s oldRun=%s nextRun=%s", oldStatus, nextStatus, reason, projectStatus, finalRun, oldRun, start.Run.ID)
	if nextStatus == "failed" && reason == "final_not_from_run" {
		t.Fatal("cancelled run's late global publication failed a newer authorized execution")
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_artifacts WHERE run_id=$1 AND kind='final'`, oldRun); n != 1 {
		t.Fatal("late old output not retained")
	}
	successorJob := claimOne(t, svc, JobAssemble)
	if successorJob.RunID != start.Run.ID {
		t.Fatal("wrong assembly owner")
	}
	if err := svc.ExecuteJob(successorJob); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdvanceRun(context.Background(), start.Run.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_runs r JOIN projects p ON p.id=r.project_id WHERE r.id=$1 AND r.status='completed' AND p.final_run_id=r.id AND COALESCE(p.final_url,'')<>''`, start.Run.ID); n != 1 {
		t.Fatal("successor never delivered")
	}
}
