package handler

// HTTP 契约：前端充值上下文经 Handler 落到 recharge_orders.run_id，
// 以及项目工作台读取最新执行状态。使用隔离测试库与模拟支付。

import (
	"database/sql"
	"testing"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
)

func mustExecDB(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}

func TestRechargeHTTPCarriesRunContext(t *testing.T) {
	t.Setenv("WXPAY_MOCK", "1")
	t.Setenv("NODE_ENV", "test")
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	svc := service.New(db, testSecret)
	h := New(svc, testSecret)
	mustExecDB(t, db, `INSERT INTO users(id,email,password_hash,nickname,credits,status) VALUES
		('u1','u1@example.com','x','U1',0,'active'),
		('u2','u2@example.com','x','U2',0,'active')`)
	mustExecDB(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status,keyvisual_url)
		VALUES('p1','u1','synthetic',100000,15,'KEYVISUAL_REVIEW','http://127.0.0.1/kv.jpg')`)
	mustExecDB(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status)
		VALUES('p2','u2','synthetic',100000,15,'KEYVISUAL_REVIEW')`)
	mustExecDB(t, db, `INSERT INTO generation_runs
		(id,user_id,project_id,status,idempotency_key,input_hash,credits_estimated) VALUES
		('r1','u1','p1','waiting_credits','k1','h1',630),
		('r2','u2','p2','waiting_credits','k2','h2',630)`)

	r := gin.New()
	a := r.Group("")
	a.Use(h.AuthMiddleware(testSecret))
	a.POST("/pay/create", h.CreatePayOrder)
	a.POST("/orders", h.CreateOrder)
	a.GET("/projects/:id/runs/latest", h.GetLatestRun)

	token := bearer(t, svc, h, "u1")

	code, b := doJSON(r, "POST", "/pay/create", `{"amountCents":10000,"channel":"native","runId":"r1"}`, token)
	if code != 200 {
		t.Fatalf("pay/create = %d %v", code, b)
	}
	data, _ := b["data"].(map[string]interface{})
	if data["runId"] != "r1" || data["id"] == "" {
		t.Fatalf("pay/create 未回传续跑上下文: %v", b)
	}
	var orderID, storedRun string
	if err := db.QueryRow(`SELECT id,COALESCE(run_id,'') FROM recharge_orders WHERE user_id='u1'`).Scan(&orderID, &storedRun); err != nil {
		t.Fatal(err)
	}
	if storedRun != "r1" {
		t.Fatalf("DB run_id = %q, want r1", storedRun)
	}

	// 其他账号的执行不得被绑定。
	code, _ = doJSON(r, "POST", "/pay/create", `{"amountCents":10000,"channel":"native","runId":"r2"}`, token)
	if code == 200 {
		t.Fatal("绑定他人执行应被拒绝")
	}

	// 人工充值入口同样携带上下文。
	code, b = doJSON(r, "POST", "/orders", `{"amountYuan":100,"runId":"r1"}`, token)
	if code != 200 {
		t.Fatalf("orders = %d %v", code, b)
	}
	if d, _ := b["data"].(map[string]interface{}); d["runId"] != "r1" {
		t.Fatalf("人工充值未保存 runId: %v", b)
	}

	// 工作台读取执行状态。
	code, b = doJSON(r, "GET", "/projects/p1/runs/latest", "", token)
	if code != 200 {
		t.Fatalf("runs/latest = %d %v", code, b)
	}
	view, _ := b["data"].(map[string]interface{})
	run, _ := view["run"].(map[string]interface{})
	if run["id"] != "r1" || view["shortfallCredits"] != float64(630) {
		t.Fatalf("latest run = %v", view)
	}
	if code, _ = doJSON(r, "GET", "/projects/p2/runs/latest", "", token); code == 200 {
		t.Fatal("非 owner 读取了执行状态")
	}
}
