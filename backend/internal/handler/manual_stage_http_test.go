package handler

import "testing"

func TestConfirmStageHTTPRoutes(t *testing.T) {
	s, db, h, r, uid := authHTTP(t)
	if _, e := db.Exec(`UPDATE users SET credits=5000 WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{
		`INSERT INTO projects(id,user_id,title,idea,status,workflow_mode,budget_credits,total_seconds) VALUES('stage-p','` + uid + `','p','synthetic','SCRIPT_REVIEW','manual',1000,5)`,
		`INSERT INTO characters(id,project_id,code,name,age_range) VALUES('stage-c','stage-p','S1','A','30')`,
		`INSERT INTO scenes(id,project_id,code,name) VALUES('stage-sc','stage-p','SC1','room')`,
		`INSERT INTO shots(id,project_id,shot_code,seq,duration,status) VALUES('stage-s','stage-p','S01',1,5,'PENDING')`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	g := r.Group("/projects/:id")
	g.Use(h.AuthMiddleware(testSecret), h.OwnProject())
	g.POST("/script/confirm", h.ConfirmScript)
	g.POST("/keyvisual/confirm", h.ConfirmKeyVisual)
	token := bearer(t, s, h, uid)
	status, body := doJSON(r, "POST", "/projects/stage-p/script/confirm", "", token)
	if status != 200 || body["ok"] != true {
		t.Fatalf("script confirm %d %#v", status, body)
	}
	var jobs int
	db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id='stage-p' AND kind='keyvisual'`).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("keyvisual jobs=%d", jobs)
	}
	if _, e := db.Exec(`UPDATE jobs SET status='done',phase='done',reserved_credits=0 WHERE project_id='stage-p' AND kind='keyvisual'; UPDATE projects SET status='KEYVISUAL_REVIEW',keyvisual_url='http://127.0.0.1/kv.jpg' WHERE id='stage-p'`); e != nil {
		t.Fatal(e)
	}
	status, body = doJSON(r, "POST", "/projects/stage-p/keyvisual/confirm", "", token)
	if status != 200 || body["ok"] != true {
		t.Fatalf("keyvisual confirm %d %#v", status, body)
	}
	db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id='stage-p' AND kind='shot'`).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("shot jobs=%d", jobs)
	}
}

func TestManualStageQueueRejectedBeforeConfirm(t *testing.T) {
	s, db, h, r, uid := authHTTP(t)
	if _, e := db.Exec(`INSERT INTO projects(id,user_id,title,idea,status,workflow_mode,budget_credits,total_seconds) VALUES('locked-p',$1,'p','synthetic','DRAFT','manual',1000,5)`, uid); e != nil {
		t.Fatal(e)
	}
	g := r.Group("/projects/:id")
	g.Use(h.AuthMiddleware(testSecret), h.OwnProject())
	g.POST("/keyvisual/queue", h.QueueKeyVisual)
	g.POST("/shots/queue", h.QueueShots)
	token := bearer(t, s, h, uid)
	if status, _ := doJSON(r, "POST", "/projects/locked-p/keyvisual/queue", `{}`, token); status != 409 {
		t.Fatalf("keyvisual before confirm status=%d", status)
	}
	if status, _ := doJSON(r, "POST", "/projects/locked-p/shots/queue", `{}`, token); status != 409 {
		t.Fatalf("shots before keyvisual status=%d", status)
	}
	var jobs int
	db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id='locked-p'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("locked stage created %d jobs", jobs)
	}
}
