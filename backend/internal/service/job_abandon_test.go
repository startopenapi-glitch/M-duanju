package service

import "testing"

// 无法核对的任务必须能被运营放弃：释放预算占用、置 failed，且不重新提交。
// 有上游身份时禁止放弃——那种情况只能核对绑定，否则会掩盖重复提交。
func TestQueueAbandonReleasesReservation(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,attempts,max_attempts,upstream_id,reserved_credits,lease_token,error_code)
	 VALUES('ab1','shot','p','s',1,'reconcile','submitting',1,2,'',275,0,'submission_unknown')`)
	mustExec(t, db, `UPDATE shots SET status='QUEUED' WHERE id='s'`)

	if e := s.AbandonJob("p", "ab1", "运营确认上游无任务"); e != nil {
		t.Fatal(e)
	}
	var status, phase string
	var reserved int64
	if e := db.QueryRow(`SELECT status,phase,reserved_credits FROM jobs WHERE id='ab1'`).Scan(&status, &phase, &reserved); e != nil {
		t.Fatal(e)
	}
	if status != "failed" || phase != "abandoned" || reserved != 0 {
		t.Fatalf("job = %s/%s reserved=%d, want failed/abandoned/0", status, phase, reserved)
	}
	var shot string
	if e := db.QueryRow(`SELECT status FROM shots WHERE id='s'`).Scan(&shot); e != nil {
		t.Fatal(e)
	}
	if shot != "PENDING" {
		t.Fatalf("shot status = %s, want PENDING（允许重新排队）", shot)
	}

	// 已有上游身份时必须拒绝放弃
	mustExec(t, db, `INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,phase,attempts,max_attempts,upstream_id,reserved_credits,lease_token)
	 VALUES('ab2','shot','p','',2,'reconcile','submitting',1,2,'up-1',275,0)`)
	if e := s.AbandonJob("p", "ab2", "x"); e == nil {
		t.Fatal("有上游身份时放弃必须被拒绝")
	}
}
