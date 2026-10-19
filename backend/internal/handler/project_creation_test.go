package handler

import (
	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
	"github.com/gin-gonic/gin"
	"testing"
)

func TestCreationHTTPReplayLookupAndOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	svc := service.New(db, testSecret)
	h := New(svc, testSecret)
	mustExecDB(t, db, `INSERT INTO users(id,email,password_hash,status,nickname) VALUES('a','a@example.com','x','active','Synthetic A'),('b','b@example.com','x','active','Synthetic B')`)
	r := gin.New()
	g := r.Group("")
	g.Use(h.AuthMiddleware(testSecret))
	g.POST("/projects", h.CreateProject)
	g.GET("/projects/creation-context", h.CreationContext)
	if code, _ := doJSON(r, "GET", "/projects/creation-context?requestKey=k", "", ""); code != 401 {
		t.Fatalf("anonymous=%d", code)
	}
	a, b := bearer(t, svc, h, "a"), bearer(t, svc, h, "b")
	body := `{"idempotencyKey":"k","idea":"synthetic story","aspect":"16:9","totalSeconds":15}`
	code, created := doJSON(r, "POST", "/projects", body, a)
	if code != 200 {
		t.Fatalf("create=%d %v", code, created)
	}
	id := created["data"].(map[string]interface{})["id"]
	code, replay := doJSON(r, "POST", "/projects", body, a)
	if code != 200 || replay["data"].(map[string]interface{})["id"] != id {
		t.Fatal("replay did not reuse original project")
	}
	code, found := doJSON(r, "GET", "/projects/creation-context?requestKey=k", "", a)
	if code != 200 || found["data"].(map[string]interface{})["id"] != id {
		t.Fatal("creation lookup missing original")
	}
	code, foreign := doJSON(r, "GET", "/projects/creation-context?requestKey=k", "", b)
	if code != 200 || foreign["data"] != nil {
		t.Fatal("cross-account creation leaked")
	}
	code, bad := doJSON(r, "POST", "/projects", `{"idea":"x","aspect":"16:9","totalSeconds":6}`, a)
	if code != 400 || bad["code"] != "project_not_created" {
		t.Fatal("definite rejection misclassified", code, bad)
	}
	mustExecDB(t, db, `CREATE FUNCTION creation_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic'; END $$; CREATE TRIGGER fail_creation BEFORE INSERT ON projects FOR EACH ROW EXECUTE PROCEDURE creation_fault()`)
	code, bad = doJSON(r, "POST", "/projects", `{"idempotencyKey":"fresh","idea":"synthetic","aspect":"16:9","totalSeconds":15}`, a)
	if code != 500 || bad["code"] == "project_not_created" {
		t.Fatal("uncertain persistence must not permit a fresh key", code, bad)
	}
}
