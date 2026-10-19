package service

import (
	"context"
	"fmt"
	"testing"
)

func TestCodexSettlementKeepsLegacyAndSiblingReservations(t *testing.T) {
	svc, db := round2PendingMoney(t, 240, 0)
	mustExec(t, db, `UPDATE users SET credits=600 WHERE id='u1';
 INSERT INTO shots(id,project_id,shot_code,seq,status,upstream_id,duration) VALUES('sib-shot','p1','S02',2,'QUEUED','sib-task',5);
 INSERT INTO jobs(id,kind,project_id,shot_id,status,phase,upstream_id,reserved_credits,run_id) VALUES('sib-job','shot','p1','sib-shot','reconcile','polling','sib-task',100,NULL);
 INSERT INTO generation_settlements(provider,kind,upstream_id,project_id,shot_id,user_id,charge_micros,credits,status,result_url) VALUES('limeng','video','sib-task','p1','sib-shot','u1',1000000,100,'pending','http://127.0.0.1/synthetic.mp4');`)
	available, err := svc.runstore.AvailableForSettlement(context.Background(), "u1", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if available != 500 {
		t.Fatalf("legacy reservation must remain: got %d want 500", available)
	}
	mustExec(t, db, `UPDATE jobs SET run_id='r1' WHERE id='sib-job'`)
	available, err = svc.runstore.AvailableForSettlement(context.Background(), "u1", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if available != 500 {
		t.Fatalf("settle one original receipt at a time: got %d want 500", available)
	}
}

func TestCodexUnderfundedWaitsRotatePastBatchLimit(t *testing.T) {
	svc, db := resumeFixture(t)
	mustExec(t, db, `DELETE FROM generation_runs WHERE id='r4'; UPDATE users SET credits=1000 WHERE id='u1'; UPDATE users SET credits=0,status='active' WHERE id='u2'; UPDATE generation_runs SET waiting_reason='balance',required_credits=200 WHERE id='r1'`)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("poor-%d", i)
		if _, err := db.Exec(`INSERT INTO projects(id,user_id,idea,total_seconds,status) VALUES($1,'u2','synthetic',5,'SCRIPT_REVIEW')`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO generation_runs(id,user_id,project_id,status,idempotency_key,input_hash,required_credits,waiting_reason,updated_at) VALUES($1,'u2',$1,'waiting_credits',$1,'x',200,'balance',now()-interval '1 hour')`, id); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		svc.AdvanceActiveRuns(context.Background(), 5)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1'`); n != 1 {
		t.Fatal("funded work starved", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs j JOIN projects p ON p.id=j.project_id WHERE p.user_id='u2'`); n != 0 {
		t.Fatal("underfunded work dispatched", n)
	}
}
