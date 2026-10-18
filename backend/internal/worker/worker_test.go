package worker

import (
	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlowKeyVisualDoesNotBlockShotLane(t *testing.T) {
	db := testutil.DB(t)
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,credits) VALUES('u','worker@example.invalid','unused',1000)`,
		`INSERT INTO projects(id,user_id,idea,budget_credits,keyvisual_url) VALUES('p','u','synthetic',1000,'image.jpg'),('p2','u','synthetic',1000,'image.jpg')`,
		`INSERT INTO shots(id,project_id,shot_code,seq,duration) VALUES('s','p2','S01',1,5)`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	image, video, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images" {
			close(image)
			<-release
			fmt.Fprint(w, `{"id":"slow-image"}`)
		} else {
			close(video)
			fmt.Fprint(w, `{"id":"fast-video"}`)
		}
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	svc := service.New(db, "synthetic")
	if _, e := svc.EnqueueJob(service.JobKeyVisual, "p", "", 0, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.GenerateShots("p2", "u", nil); e != nil {
		t.Fatal(e)
	}
	runner := New(svc)
	defer func() {
		close(release)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			runner.mu.Lock()
			active := runner.active[service.JobKeyVisual] + runner.active[service.JobShot]
			runner.mu.Unlock()
			if active == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("worker did not finish")
	}()
	tickDone := make(chan struct{})
	go func() { runner.tick(); close(tickDone) }()
	for _, ch := range []chan struct{}{image, video, tickDone} {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("slow image blocked independent shot lane")
		}
	}
}
