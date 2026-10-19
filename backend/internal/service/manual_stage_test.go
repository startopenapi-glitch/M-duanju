package service

import (
	"testing"

	"drama-platform/internal/testutil"
)

func manualStageFixture(t *testing.T) (*Service, *string) {
	t.Helper()
	db := testutil.DB(t)
	svc := New(db, "synthetic-manual-stage")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES('u','stage@example.com','unused',5000,'active')`)
	return svc, nil
}

func TestConfirmScriptRequiresReviewAndCreatesOneKeyVisual(t *testing.T) {
	svc, _ := manualStageFixture(t)
	db := svc.db
	mustExec(t, db, `
		INSERT INTO projects(id,user_id,title,idea,status,workflow_mode,budget_credits,total_seconds) VALUES
			('p','u','p','synthetic','DRAFT','manual',1000,5);
		INSERT INTO characters(id,project_id,code,name,age_range) VALUES('c','p','S1','A','30');
		INSERT INTO scenes(id,project_id,code,name) VALUES('sc','p','SC1','room');
		INSERT INTO shots(id,project_id,shot_code,seq,duration,status) VALUES('s','p','S01',1,5,'PENDING');
	`)
	if _, err := svc.ConfirmScript("u", "p"); err == nil {
		t.Fatal("DRAFT script was confirmed")
	}
	var jobs int
	db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id='p'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("early confirm created %d jobs", jobs)
	}
	mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW' WHERE id='p'`)
	id, err := svc.ConfirmScript("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("missing keyvisual job id")
	}
	var kind, status, phase string
	var reserved int64
	db.QueryRow(`SELECT kind,status,phase,reserved_credits FROM jobs WHERE id=$1`, id).Scan(&kind, &status, &phase, &reserved)
	if kind != "keyvisual" || status != "queued" || phase != "ready" || reserved != 30 {
		t.Fatalf("job=%s/%s/%s/%d", kind, status, phase, reserved)
	}
	var projectStatus string
	db.QueryRow(`SELECT status FROM projects WHERE id='p'`).Scan(&projectStatus)
	if projectStatus != "KEYVISUAL_GENERATING" {
		t.Fatalf("project status=%s", projectStatus)
	}
	if id2, err := svc.ConfirmScript("u", "p"); err != nil || id2 != id {
		t.Fatalf("idempotent confirm=%q,%v", id2, err)
	}
	db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id='p' AND kind='keyvisual'`).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("duplicate keyvisual jobs=%d", jobs)
	}
}

func TestConfirmKeyVisualCreatesShotsAndRejectsWrongStage(t *testing.T) {
	svc, _ := manualStageFixture(t)
	db := svc.db
	mustExec(t, db, `
		INSERT INTO projects(id,user_id,title,idea,status,workflow_mode,budget_credits,total_seconds,keyvisual_url) VALUES
			('p','u','p','synthetic','KEYVISUAL_GENERATING','manual',1000,10,'http://127.0.0.1/kv.jpg');
		INSERT INTO shots(id,project_id,shot_code,seq,duration,status) VALUES
			('s1','p','S01',1,5,'PENDING'),('s2','p','S02',2,5,'PENDING');
	`)
	if _, err := svc.ConfirmKeyVisual("u", "p"); err == nil {
		t.Fatal("keyvisual confirmed while still generating")
	}
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_REVIEW' WHERE id='p'`)
	n, err := svc.ConfirmKeyVisual("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("shots=%d", n)
	}
	var jobs int
	var reserved int64
	db.QueryRow(`SELECT count(*),COALESCE(sum(reserved_credits),0) FROM jobs WHERE project_id='p' AND kind='shot'`).Scan(&jobs, &reserved)
	if jobs != 2 || reserved != 400 {
		t.Fatalf("jobs=%d reserved=%d", jobs, reserved)
	}
	var status string
	db.QueryRow(`SELECT status FROM projects WHERE id='p'`).Scan(&status)
	if status != "SHOTS_GENERATING" {
		t.Fatalf("project status=%s", status)
	}
	if _, err = svc.ConfirmKeyVisual("u", "p"); err == nil {
		t.Fatal("duplicate shot dispatch accepted")
	}
	db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id='p' AND kind='shot'`).Scan(&jobs)
	if jobs != 2 {
		t.Fatalf("duplicate shots=%d", jobs)
	}
}
