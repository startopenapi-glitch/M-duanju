package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderDetailUsesMediaIDNotTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/img_1":
			json.NewEncoder(w).Encode(map[string]any{"id": "img_1", "task_id": "task_1", "status": "processing"})
		case "/videos/vid_1":
			json.NewEncoder(w).Encode(map[string]any{"id": "vid_1", "task_id": "task_1", "status": "processing"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewLiMeng()
	c.BaseURL = srv.URL
	c.APIKey = "synthetic"
	if _, err := c.GetImage("img_1"); err != nil {
		t.Fatalf("image id rejected because task_id differs: %v", err)
	}
	if _, err := c.GetVideo("vid_1"); err != nil {
		t.Fatalf("video id rejected because task_id differs: %v", err)
	}
	if _, err := c.GetImage("task_1"); err == nil {
		t.Fatal("querying the auxiliary task_id should fail")
	}
}

func TestKeyVisualSubmissionBindsMediaIDWhenTaskIDDiffers(t *testing.T) {
	s, db := queueFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "img_1", "task_id": "task_1", "status": "queued"})
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	if _, err := s.EnqueueJob(JobKeyVisual, "p", "", 0, ""); err != nil {
		t.Fatal(err)
	}
	j := claimOne(t, s, JobKeyVisual)
	if err := s.ExecuteJob(j); err != nil {
		t.Fatal(err)
	}
	var upstream, phase, status string
	if err := db.QueryRow(`SELECT COALESCE(upstream_id,''),phase,status FROM jobs WHERE id=$1`, j.ID).Scan(&upstream, &phase, &status); err != nil {
		t.Fatal(err)
	}
	if upstream != "img_1" || phase != "polling" || status != JobQueued {
		t.Fatalf("bound identity=%q phase=%q status=%q", upstream, phase, status)
	}
}
