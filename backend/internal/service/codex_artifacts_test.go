package service

import (
	"context"
	"testing"
)

func TestCodexLateVideoRetainsFactsAndExplicitlyReusesWithoutDebit(t *testing.T) {
	svc, db, in := settlementFixture(t)
	mustExec(t, db, `INSERT INTO generation_runs(id,project_id,user_id,project_version,idempotency_key,input_hash,mode,status,auto_resume) VALUES('old','p','u',1,'old','old','first','cancelled',true),('next','p','u',2,'next','next','first','queued',true);
 INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,upstream_id,run_id,reserved_credits) VALUES('oldj','shot','p','s',1,'queued','polling','task-1','old',200)`)
	j := claimOne(t, svc, JobShot)
	if ok, e := svc.withJob(j).SettleGeneration(in); e != nil || !ok {
		t.Fatalf("late settlement: %v %v", ok, e)
	}
	if n := countRows(t, db, `SELECT count(*) FROM shots WHERE id='s' AND status='QUEUED' AND COALESCE(video_url,'')=''`); n != 1 {
		t.Fatal("old result published into successor before explicit reuse")
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_artifacts WHERE run_id='old' AND job_id='oldj' AND result_url=$1`, in.URL); n != 1 {
		t.Fatal("old artifact not retained")
	}
	if e := svc.FinishJob(j.ID, j.LeaseToken, in.TaskID); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := svc.adoptRunArtifacts(context.Background(), "next", "p"); e != nil {
			t.Fatal(e)
		}
	}
	var duration float64
	var width, height int
	var micros int64
	if e := db.QueryRow(`SELECT duration_actual,width,height,charge_micros FROM shots WHERE id='s'`).Scan(&duration, &width, &height, &micros); e != nil {
		t.Fatal(e)
	}
	if duration != in.Duration || width != in.Width || height != in.Height || micros != in.ChargeMicros {
		t.Fatalf("reuse lost verified metadata: %.2f %d %d %d", duration, width, height, micros)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_artifact_reuses WHERE run_id='next'`); n != 1 {
		t.Fatal(n)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
