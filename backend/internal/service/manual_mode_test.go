package service

import (
	"context"
	"testing"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func TestStopAllAutomaticRunsPreservesSubmittedWork(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic-manual-mode")
	mustExec(t, db, `
		INSERT INTO users(id,email,password_hash,credits,status) VALUES('u','manual@example.com','unused',1000,'active');
		INSERT INTO projects(id,user_id,title,idea,status,workflow_mode) VALUES
			('p1','u','p1','synthetic','SHOTS_GENERATING','auto'),
			('p2','u','p2','synthetic','SCRIPT_REVIEW','auto');
		INSERT INTO generation_runs(id,user_id,project_id,status,idempotency_key,input_hash) VALUES
			('r1','u','p1','running','k1','h1'),
			('r2','u','p2','waiting_credits','k2','h2');
		INSERT INTO shots(id,project_id,shot_code,seq,status) VALUES
			('s1','p1','S01',1,'QUEUED'),('s2','p1','S02',2,'QUEUED'),('s3','p1','S03',3,'MANUAL_REQUIRED');
		INSERT INTO jobs(id,kind,project_id,shot_id,run_id,status,phase,reserved_credits) VALUES
			('j-unsubmitted','shot','p1','s1','r1','queued','ready',200),
			('j-submitted','shot','p1','s2','r1','queued','polling',200),
			('j-keyvisual','keyvisual','p1',NULL,'r1','queued','ready',30),
			('j-reconcile','shot','p1','s3','r1','reconcile','submitting',200);
		UPDATE jobs SET upstream_id='vid-kept' WHERE id='j-submitted';
	`)
	res, err := svc.StopAllAutomaticRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Cancelled != 2 || len(res.Failed) != 0 {
		t.Fatalf("result=%+v", res)
	}
	var runs, queued, submitted, keyvisual, reconcile int
	var reserved int64
	db.QueryRow(`SELECT count(*) FROM generation_runs WHERE status='cancelled'`).Scan(&runs)
	db.QueryRow(`SELECT count(*) FROM jobs WHERE id='j-unsubmitted' AND status='cancelled' AND reserved_credits=0`).Scan(&queued)
	db.QueryRow(`SELECT count(*) FROM jobs WHERE id='j-submitted' AND status='queued' AND upstream_id='vid-kept'`).Scan(&submitted)
	db.QueryRow(`SELECT count(*) FROM jobs WHERE id='j-keyvisual' AND status='cancelled' AND reserved_credits=0`).Scan(&keyvisual)
	db.QueryRow(`SELECT count(*) FROM jobs WHERE id='j-reconcile' AND status='reconcile'`).Scan(&reconcile)
	db.QueryRow(`SELECT count(*) FROM projects WHERE workflow_mode='manual'`).Scan(&runs)
	db.QueryRow(`SELECT COALESCE(sum(reserved_credits),0) FROM jobs WHERE status IN ('queued','running','reconcile') AND upstream_id=''`).Scan(&reserved)
	if runs != 2 || queued != 1 || submitted != 1 || keyvisual != 1 || reconcile != 1 || reserved != 0 {
		t.Fatalf("runs=%d unsubmitted=%d submitted=%d keyvisual=%d reconcile=%d reserved=%d", runs, queued, submitted, keyvisual, reconcile, reserved)
	}
	// Re-running the one-time stop must not create failures or alter terminal work.
	again, err := svc.StopAllAutomaticRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Cancelled != 0 || len(again.Failed) != 0 {
		t.Fatalf("second stop=%+v", again)
	}
	db.QueryRow(`SELECT count(*) FROM jobs WHERE id='j-reconcile' AND status='reconcile'`).Scan(&reconcile)
	if reconcile != 1 {
		t.Fatal("second stop changed reconcile job")
	}
	var audit int
	db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='stop_automatic_mode'`).Scan(&audit)
	if audit != 1 {
		t.Fatalf("audit=%d", audit)
	}
}

func TestNewProjectStartsManualWithoutRun(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic-manual-create")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES('u','create@example.com','unused',5000,'active')`)
	p, err := svc.CreateProject("u", &model.CreateProjectReq{IdempotencyKey: "manual-create", Idea: "synthetic", Aspect: "16:9", TotalSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if p.WorkflowMode != "manual" {
		t.Fatalf("workflow_mode=%q", p.WorkflowMode)
	}
	var runs int
	db.QueryRow(`SELECT count(*) FROM generation_runs WHERE project_id=$1`, p.ID).Scan(&runs)
	if runs != 0 {
		t.Fatalf("manual project created %d runs", runs)
	}
}
