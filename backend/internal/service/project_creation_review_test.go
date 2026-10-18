package service

import (
	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
	"encoding/json"
	"sync"
	"testing"
)

func TestProjectCreationResponseLossAndConcurrentReplay(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits) VALUES('create-u','create@example.invalid','x',10000)`)
	var req model.CreateProjectReq
	if e := json.Unmarshal([]byte(`{"idempotencyKey":"create-request","idea":"synthetic story","aspect":"16:9","totalSeconds":15}`), &req); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 10)
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := svc.CreateProject("create-u", &req)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- p.ID
		}()
	}
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("same creation request made %d projects, want 1", len(unique))
	}
	if n := countRows(t, db, `SELECT count(*) FROM projects`); n != 1 {
		t.Fatalf("projects=%d", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM users WHERE id='create-u' AND credits=10000`); n != 1 {
		t.Fatal("creation changed wallet")
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs`); n != 0 {
		t.Fatal("creation dispatched generation")
	}
	req.Idea = "a different story"
	if _, e := svc.CreateProject("create-u", &req); e == nil {
		t.Fatal("same key accepted changed input")
	}
}

func TestProjectCreationIdentityScopeDeletionAndLegacy(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash) VALUES('one','one@example.invalid','x'),('two','two@example.invalid','x')`)
	req := &model.CreateProjectReq{Idea: "synthetic story", Aspect: "16:9", TotalSeconds: 15, IdempotencyKey: "same-key"}
	p, e := svc.CreateProject("one", req)
	if e != nil {
		t.Fatal(e)
	}
	if got, e := svc.CreationContext("two", req.IdempotencyKey); e != nil || got != nil {
		t.Fatal("foreign identity leaked", e)
	}
	other, e := svc.CreateProject("two", req)
	if e != nil || other.ID == p.ID {
		t.Fatal("account identity collision", e)
	}
	if _, e = db.Exec(`DELETE FROM projects WHERE id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.CreateProject("one", req); e == nil {
		t.Fatal("deleted project recreated on delayed retry")
	}
	if _, e = svc.CreationContext("one", req.IdempotencyKey); e == nil {
		t.Fatal("deleted request lost tombstone")
	}
	req.IdempotencyKey = ""
	a, e := svc.CreateProject("one", req)
	if e != nil {
		t.Fatal(e)
	}
	b, e := svc.CreateProject("one", req)
	if e != nil || a.ID == b.ID {
		t.Fatal("legacy creates accidentally merged", e)
	}
}

func TestProjectCreationRequestFailureRollsBackProject(t *testing.T) {
	db := testutil.DB(t)
	svc := New(db, "synthetic")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash) VALUES('u','u@example.invalid','x');
 CREATE FUNCTION reject_creation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic creation fault'; END $$;
 CREATE TRIGGER fail_creation BEFORE INSERT ON project_creation_requests FOR EACH ROW EXECUTE PROCEDURE reject_creation()`)
	req := &model.CreateProjectReq{Idea: "synthetic", Aspect: "16:9", TotalSeconds: 15, IdempotencyKey: "request"}
	if _, e := svc.CreateProject("u", req); e == nil {
		t.Fatal("expected injected transaction failure")
	}
	if n := countRows(t, db, `SELECT count(*) FROM projects`); n != 0 {
		t.Fatal("orphan project after request identity failure")
	}
	mustExec(t, db, `DROP TRIGGER fail_creation ON project_creation_requests`)
	if _, e := svc.CreateProject("u", req); e != nil {
		t.Fatal(e)
	}
	if n := countRows(t, db, `SELECT count(*) FROM project_creation_requests`); n != 1 {
		t.Fatal("missing replay identity")
	}
}
