package service

import (
	"testing"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func runFixture(t *testing.T, credits int64) (*Service, func(string, string, string) (*StartRunResult, error)) {
	t.Helper()
	db := testutil.DB(t)
	svc := New(db, "synthetic-test-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('u1','u1@example.com','x',`+
		func() string { return "0" }()+`)`)
	mustExec(t, db, `UPDATE users SET credits=`+itoa(credits)+` WHERE id='u1'`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status)
	 VALUES('p1','u1','synthetic',10000,15,'KEYVISUAL_REVIEW')`)
	return svc, func(uid, pid, key string) (*StartRunResult, error) {
		return svc.StartRun(uid, pid, &StartRunReq{IdempotencyKey: key})
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// 积分足够：直接进入 queued，不算等待
func TestStartRunProceedsWhenAffordable(t *testing.T) {
	_, start := runFixture(t, 10000)
	res, err := start("u1", "p1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if res.WaitingForCredits || res.ShortfallCredits != 0 {
		t.Fatalf("应直接执行，实际 waiting=%v shortfall=%d", res.WaitingForCredits, res.ShortfallCredits)
	}
	if res.Run.Status != "queued" {
		t.Fatalf("状态 = %s, want queued", res.Run.Status)
	}
}

// 积分不足：仍必须持久化执行记录并进入等待态
func TestStartRunPersistsWhenInsufficient(t *testing.T) {
	_, start := runFixture(t, 10)
	res, err := start("u1", "p1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.WaitingForCredits || res.ShortfallCredits <= 0 {
		t.Fatalf("应进入等待，实际 waiting=%v shortfall=%d", res.WaitingForCredits, res.ShortfallCredits)
	}
	if res.Run.Status != "waiting_credits" {
		t.Fatalf("状态 = %s, want waiting_credits", res.Run.Status)
	}
}

// 幂等：同键同输入返回同一 run
func TestStartRunIsIdempotent(t *testing.T) {
	_, start := runFixture(t, 10000)
	a, err := start("u1", "p1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := start("u1", "p1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Run.ID != b.Run.ID {
		t.Fatalf("同键应返回同一 run：%s vs %s", a.Run.ID, b.Run.ID)
	}
}

// 越权：不能为别人的项目发起生成
func TestStartRunRejectsForeignProject(t *testing.T) {
	_, start := runFixture(t, 10000)
	if _, err := start("someone-else", "p1", "k1"); err == nil {
		t.Fatal("非项目所有者必须被拒绝")
	}
}

// 客户不再填写预算：创建项目按服务端范围计算上限，忽略客户端传值。
func TestCreateProjectIgnoresClientBudget(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic-test-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('u1','u1@example.com','x',10000)`)
	p, err := svc.CreateProject("u1", &model.CreateProjectReq{
		Idea: "synthetic", Aspect: "16:9", TotalSeconds: 15, BudgetYuan: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.BudgetCredits != 1260 {
		t.Fatalf("budget = %d, want 1260（按范围计算，忽略客户端 500 元）", p.BudgetCredits)
	}
}

// 历史项目停在旧预算门槛时，一次「继续生成」授权按范围补足服务端上限。
func TestStartRunRaisesLegacyBudgetWithinScope(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic-test-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('u1','u1@example.com','x',10000)`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,spent_credits,total_seconds,status)
		VALUES('p1','u1','synthetic',300,0,15,'BUDGET_EXCEEDED')`)
	res, err := svc.StartRun("u1", "p1", &StartRunReq{IdempotencyKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	var budget int64
	if err := db.QueryRow(`SELECT budget_credits FROM projects WHERE id='p1'`).Scan(&budget); err != nil {
		t.Fatal(err)
	}
	if budget != 1260 {
		t.Fatalf("budget = %d, want 1260", budget)
	}
	view, err := svc.LatestRun("u1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if view == nil || view.Run.ID != res.Run.ID || view.AvailableCredits != 10000 {
		t.Fatalf("LatestRun = %+v", view)
	}
	// 非 owner 不得读执行状态。
	if _, err := svc.LatestRun("u2", "p1"); err == nil {
		t.Fatal("非 owner 读取了执行状态")
	}
}
