package handler

import "testing"

func TestDeleteProjectHTTP(t *testing.T) {
	s, db, h, r, uid := authHTTP(t)
	if _, e := db.Exec(`INSERT INTO projects(id,user_id,title,idea,status) VALUES('delete-me',$1,'old','synthetic','COMPLETED')`, uid); e != nil {
		t.Fatal(e)
	}
	g := r.Group("/projects/:id")
	g.Use(h.AuthMiddleware(testSecret), h.OwnProject())
	g.DELETE("", h.DeleteProject)
	g.GET("", h.GetProject)
	token := bearer(t, s, h, uid)
	if status, b := doJSON(r, "DELETE", "/projects/delete-me", "", token); status != 200 || b["ok"] != true {
		t.Fatalf("delete %d %#v", status, b)
	}
	if status, _ := doJSON(r, "GET", "/projects/delete-me", "", token); status != 404 {
		t.Fatalf("deleted project still readable: %d", status)
	}
	if status, _ := doJSON(r, "DELETE", "/projects/delete-me", "", token); status != 404 {
		t.Fatalf("second delete status %d", status)
	}
}
