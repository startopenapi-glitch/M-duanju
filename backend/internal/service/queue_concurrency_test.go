package service

import (
	"sync"
	"testing"

	"drama-platform/internal/testutil"
)

// R1：同一用户的可用积分只能被一个并发入队消费。
// lockQueueProject 对 users 行加 FOR UPDATE，预占 INSERT 在同一事务内，
// 因此第二个并发调用会等待锁、重读预占合计后失败——不会超额占用。
func TestQueueConcurrentReservationsCannotOverspend(t *testing.T) {
	db := testutil.DB(t)
	s := New(db, "synthetic-test-secret")

	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('u1','u1@example.invalid','x',300)`)
	for i, pid := range []string{"p1", "p2"} {
		mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,status,keyvisual_url)
		 VALUES('`+pid+`','u1','synthetic',10000,'KEYVISUAL_REVIEW','http://127.0.0.1/kv.jpg')`)
		mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,status,duration)
		 VALUES('s`+string(rune('1'+i))+`','`+pid+`','S01',1,'PENDING',5)`)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, pid := range []string{"p1", "p2"} {
		wg.Add(1)
		go func(project string) {
			defer wg.Done()
			_, _, err := s.queueShots(project, "", []int{1}, true, false, "")
			errs <- err
		}(pid)
	}
	wg.Wait()
	close(errs)

	okCount := 0
	var msgs []string
	for err := range errs {
		if err == nil {
			okCount++
		} else {
			msgs = append(msgs, err.Error())
		}
	}
	if okCount != 1 {
		t.Fatalf("并发入队成功 %d 次，应为 1（余额只够一段）；错误：%v", okCount, msgs)
	}

	var reserved int64
	if err := db.QueryRow(`SELECT COALESCE(sum(reserved_credits),0) FROM jobs`).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved > 300 {
		t.Fatalf("预占合计 %d 超过余额 300，出现超额占用", reserved)
	}
}
