package service

import (
	"drama-platform/internal/model"
	"fmt"
	"testing"
)

func TestProjectListFilterCountsAndScopesAllPages(t *testing.T) {
	s := queryFixture(t)
	mustExec(t, s.db, `INSERT INTO users(id,email,password_hash) VALUES('other','other@example.test','x')`)
	for i := 0; i < 24; i++ {
		if _,e:=s.db.Exec(`INSERT INTO projects(id,user_id,title,idea,status) VALUES($1,'uq','needle 50%_','test','DRAFT')`, fmt.Sprint("filter", i));e!=nil{t.Fatal(e)}
	}
	mustExec(t, s.db, `INSERT INTO projects(id,user_id,title,idea) VALUES('foreign','other','needle 50%_','test')`)
	p, e := s.ListProjectsFiltered("uq", 2, 20, ProjectListFilter{Query: "50%_", Status: "DRAFT"})
	if e != nil {
		t.Fatal(e)
	}
	if p.Total != 24 || len(p.Items.([]*model.Project)) != 4 {
		t.Fatalf("bad scoped pagination: %#v", p)
	}
	p, e = s.ListProjectsFiltered("uq", 1, 20, ProjectListFilter{Query: "50X_"})
	if e != nil || p.Total != 0 {
		t.Fatal("LIKE wildcard leaked", e, p)
	}
	for _, f := range []ProjectListFilter{{Status: "bad"}, {Query: string(make([]byte, 101))}} {
		if _, e = s.ListProjectsFiltered("uq", 1, 20, f); e == nil {
			t.Fatal("invalid filter accepted")
		}
	}
}
