package service

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"drama-platform/internal/testutil"
)

func deleteFixture(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := testutil.DB(t)
	s := New(db, "synthetic-delete-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES('u','delete@example.com','unused',1000,'active');
		INSERT INTO projects(id,user_id,title,idea,status,spent_credits) VALUES('p','u','old','synthetic','COMPLETED',200)`)
	return s, db
}

func TestDeleteProjectSoftDeletesAndPreservesFinancialFacts(t *testing.T) {
	s, db := deleteFixture(t)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,status) VALUES('s','p','S01',0,'APPROVED');
		INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url)
		VALUES('limeng','video','task-delete','p','s','u',2000000,200,'settled','https://example.com/f.mp4');
		INSERT INTO credit_transactions(id,user_id,amount,balance_after,kind,project_id) VALUES('tx-delete','u',-200,800,'spend','p')`)
	if err := s.DeleteProject("u", "p", false); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	var deletedBy string
	if err := db.QueryRow(`SELECT deleted_at IS NOT NULL,COALESCE(deleted_by,'') FROM projects WHERE id='p'`).Scan(&deleted, &deletedBy); err != nil {
		t.Fatal(err)
	}
	if !deleted || deletedBy != "u" {
		t.Fatalf("deleted=%v by=%q", deleted, deletedBy)
	}
	page, err := s.ListProjectsFiltered("u", 1, 20, ProjectListFilter{})
	if err != nil || page.Total != 0 {
		t.Fatalf("deleted project still listed: %+v %v", page, err)
	}
	if _, err := s.GetProject("p"); err == nil {
		t.Fatal("deleted project still readable")
	}
	if _, err := s.ProjectOwner("p"); err == nil {
		t.Fatal("deleted project still owned")
	}
	var settlements, transactions, audits int
	db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements WHERE project_id='p'),(SELECT count(*) FROM credit_transactions WHERE project_id='p'),(SELECT count(*) FROM audit_logs WHERE project_id='p' AND action='delete_project')`).Scan(&settlements, &transactions, &audits)
	if settlements != 1 || transactions != 1 || audits != 1 {
		t.Fatalf("financial/audit facts lost: settlements=%d tx=%d audits=%d", settlements, transactions, audits)
	}
	if err := s.DeleteProject("u", "p", false); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestDeleteProjectRejectsActiveWorkAndForeignOwner(t *testing.T) {
	s, db := deleteFixture(t)
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING' WHERE id='p'; INSERT INTO jobs(id,kind,project_id,status) VALUES('j-delete','keyvisual','p','queued')`)
	if err := s.DeleteProject("u", "p", false); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("active delete = %v", err)
	}
	mustExec(t, db, `DELETE FROM jobs WHERE project_id='p'; UPDATE projects SET status='COMPLETED' WHERE id='p'`)
	if err := s.DeleteProject("other", "p", false); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("foreign delete = %v", err)
	}
	var deleted bool
	db.QueryRow(`SELECT deleted_at IS NOT NULL FROM projects WHERE id='p'`).Scan(&deleted)
	if deleted {
		t.Fatal("foreign/active project was deleted")
	}
}

func TestDeleteProjectForceAllowsReconcileButKeepsJobs(t *testing.T) {
	s, db := deleteFixture(t)
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING' WHERE id='p';
		INSERT INTO jobs(id,kind,project_id,status,phase,error_code) VALUES('j-reconcile','keyvisual','p','reconcile','submitting','submission_unknown')`)
	if err := s.DeleteProject("u", "p", false); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("normal delete with reconcile = %v", err)
	}
	if err := s.DeleteProject("u", "p", true); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	var jobs int
	db.QueryRow(`SELECT deleted_at IS NOT NULL,(SELECT count(*) FROM jobs WHERE project_id='p') FROM projects WHERE id='p'`).Scan(&deleted, &jobs)
	if !deleted || jobs != 1 {
		t.Fatalf("forced delete deleted=%v jobs=%d", deleted, jobs)
	}
	var note string
	db.QueryRow(`SELECT note FROM audit_logs WHERE project_id='p' AND action='delete_project' ORDER BY created_at DESC LIMIT 1`).Scan(&note)
	if !strings.Contains(note, "forced") {
		t.Fatalf("audit note=%q", note)
	}
}
