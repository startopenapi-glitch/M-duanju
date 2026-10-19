package service

import "testing"

// 旧活动任务（010 之前入队）没有 reserved_credits，会让并发预算校验少算占用。
// 验证 010_job_leases.sql 里的回填语句把占用补齐，且不碰已完成任务。
func TestQueueLegacyActiveJobsBackfillReservations(t *testing.T) {
	_, db, _ := settlementFixture(t)

	// 直接构造一条 legacy 形态的活动任务：占用为 0、无 lease、提交阶段
	mustExec(t, db, `INSERT INTO jobs(id,kind,project_id,shot_id,seq,status,attempts,max_attempts,reserved_credits,lease_token,phase)
	 VALUES('legacyjob','shot','p','s',1,'running',0,2,0,0,'submitting')`)

	// 与 migrations/010_job_leases.sql 中的语句一致
	mustExec(t, db, `UPDATE jobs j SET reserved_credits = GREATEST(80, FLOOR(s.duration * 40)::bigint)
	 FROM shots s
	 WHERE j.kind='shot' AND j.shot_id=s.id
	   AND j.status IN ('queued','running','reconcile') AND j.reserved_credits=0`)
	mustExec(t, db, `UPDATE jobs SET reserved_credits = 30
	 WHERE kind='keyvisual' AND status IN ('queued','running','reconcile') AND reserved_credits=0`)

	var got, want int64
	if e := db.QueryRow(`SELECT reserved_credits FROM jobs WHERE id='legacyjob'`).Scan(&got); e != nil {
		t.Fatal(e)
	}
	if e := db.QueryRow(`SELECT GREATEST(80, FLOOR(duration * 40)::bigint) FROM shots WHERE id='s'`).Scan(&want); e != nil {
		t.Fatal(e)
	}
	if got != want {
		t.Fatalf("backfilled reservation = %d, want %d (720P rate)", got, want)
	}
	if got <= 0 {
		t.Fatalf("backfilled reservation must be positive, got %d", got)
	}

	// 已完成任务不应被回填，避免历史成功任务重新算成占用
	mustExec(t, db, `UPDATE jobs SET reserved_credits=0,status='done' WHERE id='legacyjob'`)
	mustExec(t, db, `UPDATE jobs j SET reserved_credits = GREATEST(80, FLOOR(s.duration * 40)::bigint)
	 FROM shots s
	 WHERE j.kind='shot' AND j.shot_id=s.id
	   AND j.status IN ('queued','running','reconcile') AND j.reserved_credits=0`)
	var still int64
	if e := db.QueryRow(`SELECT reserved_credits FROM jobs WHERE id='legacyjob'`).Scan(&still); e != nil {
		t.Fatal(e)
	}
	if still != 0 {
		t.Fatalf("completed job must not be backfilled, got %d", still)
	}
}
