package handler

import "testing"

func TestStartRunRejectsManualProject(t *testing.T) {
	s, db, h, r, uid := authHTTP(t)
	if _, e := db.Exec(`INSERT INTO projects(id,user_id,title,idea,status,workflow_mode,budget_credits,total_seconds) VALUES('manual-p',$1,'p','synthetic','SCRIPT_REVIEW','manual',1000,5)`, uid); e != nil {
		t.Fatal(e)
	}
	g := r.Group("/projects/:id")
	g.Use(h.AuthMiddleware(testSecret))
	g.POST("/runs", h.StartRun)
	token := bearer(t, s, h, uid)
	status, body := doJSON(r, "POST", "/projects/manual-p/runs", `{"idempotencyKey":"manual-start"}`, token)
	if status != 409 || body["code"] != "manual_mode" {
		t.Fatalf("manual start status=%d body=%#v", status, body)
	}
}
