package service

import "testing"

// 人工绑定上游身份：必须拒绝「已被其它任务占用的身份」和「跨项目的任务」，
// 否则两个项目会指向同一个付费任务，或越权改写他人项目。
func TestQueueReconcileRejectsCrossProjectAndDuplicateIdentity(t *testing.T) {
	s, db, _ := settlementFixture(t)

	mustExec(t, db, `INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,attempts,max_attempts,upstream_id,reserved_credits,lease_token)
	 VALUES('j1','shot','p','s',1,'reconcile','submitting',1,2,'',0,0)`)
	mustExec(t, db, `INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,attempts,max_attempts,upstream_id,reserved_credits,lease_token)
	 VALUES('j2','shot','p','',2,'done','delivered',1,2,'task-9',0,0)`)

	// 1) 身份已被别的任务占用 -> 拒绝
	if e := s.ReconcileSubmission("p", "j1", "task-9"); e == nil {
		t.Fatal("expected rejection: provider identity already bound")
	}
	// 2) 任务不属于该项目 -> 拒绝（越权保护）
	if e := s.ReconcileSubmission("other", "j1", "task-8"); e == nil {
		t.Fatal("expected rejection: job not in project")
	}
	// 3) 正例：合法绑定后进入 polling，且仍占用预算
	if e := s.ReconcileSubmission("p", "j1", "task-8"); e != nil {
		t.Fatalf("unexpected rejection: %v", e)
	}
	var status, phase, up string
	if e := db.QueryRow(`SELECT status,phase,COALESCE(upstream_id,'') FROM jobs WHERE id='j1'`).Scan(&status, &phase, &up); e != nil {
		t.Fatal(e)
	}
	if status != "queued" || phase != "polling" || up != "task-8" {
		t.Fatalf("got %s/%s/%s, want queued/polling/task-8", status, phase, up)
	}
	// 4) 已被绑定的身份不能再次绑定到同一任务以外的地方
	if e := s.ReconcileSubmission("p", "j1", "task-8"); e == nil {
		t.Fatal("expected rejection: already reconciled")
	}
}
