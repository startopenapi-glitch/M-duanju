package handler

import (
	"fmt"
	"testing"
)

func TestGenerationStructuredPreflightStatusAndResume(t *testing.T) {
	s, db, h, r, uid := authHTTP(t)
	if _, e := db.Exec(`UPDATE users SET credits=1000 WHERE id=$1; `, uid); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO projects(id,user_id,idea,budget_credits,keyvisual_url) VALUES('p',$1,'synthetic',100,'image.jpg'),('p2',$1,'synthetic',1000,'image.jpg')`, uid); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES('s','p','S01',1,5)`); e != nil {
		t.Fatal(e)
	}
	group := r.Group("/projects/:id")
	group.Use(h.AuthMiddleware(testSecret), h.OwnProject())
	group.POST("/shots/queue", h.QueueShots)
	group.GET("", h.GetProject)
	group.POST("/jobs/:jid/resume", h.ResumeGenerationJob)
	token := bearer(t, s, h, uid)
	status, b := doJSON(r, "POST", "/projects/p/shots/queue", `{}`, token)
	if status != 429 || b["code"] != "budget_insufficient" || b["requiredCredits"] != float64(200) || b["availableCredits"] != float64(100) {
		t.Fatalf("preflight %d %+v", status, b)
	}
	var jobs, retries int
	db.QueryRow(`SELECT (SELECT count(*) FROM jobs),(SELECT retry_count FROM shots WHERE id='s')`).Scan(&jobs, &retries)
	if jobs != 0 || retries != 0 {
		t.Fatal("rejected preflight created work")
	}
	if _, e := db.Exec(`UPDATE projects SET budget_credits=1000 WHERE id='p'`); e != nil {
		t.Fatal(e)
	}
	if status, b = doJSON(r, "POST", "/projects/p/shots/queue", `{}`, token); status != 200 {
		t.Fatalf("queue %d %+v", status, b)
	}
	status, b = doJSON(r, "GET", "/projects/p", "", token)
	if status != 200 {
		t.Fatal(status, b)
	}
	data := b["data"].(map[string]interface{})
	list := data["generationJobs"].([]interface{})
	if len(list) != 1 || data["reservedCredits"] != float64(200) {
		t.Fatalf("contract %+v", data)
	}
	job := list[0].(map[string]interface{})
	if _, exists := job["leaseToken"]; exists {
		t.Fatal("lease token exposed")
	}
	id := job["id"].(string)
	if _, e := db.Exec(`UPDATE jobs SET status='reconcile',phase='submitting',error_code='submission_unknown'`); e != nil {
		t.Fatal(e)
	}
	if status, b = doJSON(r, "POST", fmt.Sprintf("/projects/p/jobs/%s/resume", id), "{}", token); status != 409 || b["code"] != "submission_unknown" {
		t.Fatalf("unknown resume %d %+v", status, b)
	}
	if _, e := db.Exec(`UPDATE jobs SET upstream_id='known',phase='polling',error_code='settlement_budget'`); e != nil {
		t.Fatal(e)
	}
	if status, _ = doJSON(r, "POST", fmt.Sprintf("/projects/p2/jobs/%s/resume", id), "{}", token); status == 200 {
		t.Fatal("cross-project job resumed")
	}
	if status, b = doJSON(r, "POST", fmt.Sprintf("/projects/p/jobs/%s/resume", id), "{}", token); status != 200 {
		t.Fatalf("known resume %d %+v", status, b)
	}
}
