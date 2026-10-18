package service

import (
	"drama-platform/internal/model"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticScript = `{"title":"隔离样片","characters":[{"code":"S1","appearance":"a","wardrobe":"w","persona":"p","voice_profile":"v"},{"code":"S2","appearance":"a","wardrobe":"w","persona":"p","voice_profile":"v"}],"scenes":[{"code":"SC1","props":[]}],"shots":[{"shot_code":"Shot 1","scene_code":"SC1","duration":5,"camera_move":"固定","visual_prompt":"暖光照在桌面，画面左侧人物转身看向窗口，保持固定机位","speaker_code":"S1","dialogue":"你好"}]}`

func TestScriptRetryProtectsArtifacts(t *testing.T) {
	for _, change := range []string{
		`UPDATE projects SET final_url='paid.mp4'`, `UPDATE projects SET spent_credits=1`, `UPDATE projects SET keyvisual_url='paid.jpg'`,
		`UPDATE shots SET video_url='paid.mp4'`, `UPDATE shots SET status='APPROVED'`, `UPDATE shots SET upstream_id='paid-task'`,
		`INSERT INTO jobs(kind,project_id,shot_id,seq,status) VALUES('shot','p','s',1,'done')`,
	} {
		t.Run(change, func(t *testing.T) {
			s, db := queueFixture(t)
			mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW',total_seconds=5,keyvisual_url=NULL;`+change)
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]string{"content": syntheticScript}}}})
			}))
			defer srv.Close()
			t.Setenv("LIMENG_BASE_URL", srv.URL)
			_, _, e := s.GenerateScript("p", nil)
			if e == nil {
				t.Error("destructive retry accepted")
			}
			if calls != 0 {
				t.Error("protected project called provider")
			}
			var n int
			db.QueryRow(`SELECT count(*) FROM shots WHERE id='s'`).Scan(&n)
			if n != 1 {
				t.Fatal("old shot deleted")
			}
		})
	}
}
func TestScriptConcurrentClaimAndFailedRecovery(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW',total_seconds=5,keyvisual_url=NULL`)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var fail int32 = 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&fail) == 1 {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			w.WriteHeader(503)
			w.Write([]byte("sensitive-provider-body"))
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]string{"content": syntheticScript}}}})
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	a, b := make(chan error, 1), make(chan error, 1)
	go func() { _, _, e := s.GenerateScript("p", nil); a <- e }()
	select {
	case <-entered:
	case e := <-a:
		t.Fatalf("initial generation failed: %v", e)
	case <-time.After(3 * time.Second):
		t.Fatal("provider not called")
	}
	go func() { _, _, e := s.GenerateScript("p", nil); b <- e }()
	select {
	case e := <-b:
		if e == nil {
			t.Error("concurrent accepted")
		}
	case <-time.After(150 * time.Millisecond):
		t.Error("second caller not rejected before provider")
	}
	close(release)
	e := <-a
	if e == nil || strings.Contains(e.Error(), "sensitive-provider-body") {
		t.Error("unsafe failure", e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM shots WHERE id='s'`).Scan(&n)
	if n != 1 {
		t.Error("failure removed old draft")
	}
	atomic.StoreInt32(&fail, 0)
	if _, _, e = s.GenerateScript("p", nil); e != nil {
		t.Fatal(e)
	}
	var status string
	db.QueryRow(`SELECT status FROM projects WHERE id='p'`).Scan(&status)
	if status != "SCRIPT_REVIEW" {
		t.Fatal(status)
	}
	// Repeat replacement exercises scene FK deletion ordering without losing original on error.
	if _, _, e = s.GenerateScript("p", nil); e != nil {
		t.Fatal(e)
	}
}
func TestScriptLeaseFenceAndTransactionalReplacement(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW',keyvisual_url=NULL,total_seconds=5`)
	first, e := s.claimScript("p")
	if e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE projects SET script_lease_until=now()-interval '1 minute'`)
	second, e := s.claimScript("p")
	if e != nil {
		t.Fatal(e)
	}
	script, e := parseScriptJSON(syntheticScript)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.saveScript("p", first, script); e == nil {
		t.Fatal("late owner replaced script")
	}
	if _, e = s.EnqueueJob(JobKeyVisual, "p", "", 0, ""); e == nil {
		t.Fatal("image queued while script generating")
	}
	mustExec(t, db, `CREATE FUNCTION reject_script_shot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic insert failure'; END $$;CREATE TRIGGER reject_script BEFORE INSERT ON shots FOR EACH ROW EXECUTE PROCEDURE reject_script_shot()`)
	if e = s.saveScript("p", second, script); e == nil {
		t.Fatal("failed insert accepted")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM shots WHERE id='s'`).Scan(&n)
	if n != 1 {
		t.Fatal("failed replacement deleted draft")
	}
	mustExec(t, db, `DROP TRIGGER reject_script ON shots`)
	if e = s.saveScript("p", second, script); e != nil {
		t.Fatal(e)
	}
	s.releaseScript("p", first)
	var status string
	db.QueryRow(`SELECT status FROM projects WHERE id='p'`).Scan(&status)
	if status != "SCRIPT_REVIEW" {
		t.Fatal("stale failure changed status")
	}
}
func TestProjectListPaginationAndStoredScriptPreferences(t *testing.T) {
	s, db := queueFixture(t)
	for i := 0; i < 24; i++ {
		_, e := s.CreateProject("u", &model.CreateProjectReq{Idea: "synthetic", Aspect: "16:9", TotalSeconds: 5, BudgetYuan: 5, FilmRef: "stored film", Mood: "stored mood"})
		if e != nil {
			t.Fatal(e)
		}
	}
	a, e := s.ListProjects("u", 1, 20)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.ListProjects("u", 2, 20)
	if e != nil {
		t.Fatal(e)
	}
	if a.Total != 25 || len(a.Items.([]*model.Project)) != 20 || len(b.Items.([]*model.Project)) != 5 {
		t.Fatal("truncated pagination")
	}
	seen := map[string]bool{}
	for _, p := range append(a.Items.([]*model.Project), b.Items.([]*model.Project)...) {
		if seen[p.ID] {
			t.Fatal("duplicate page row")
		}
		seen[p.ID] = true
	}
	var film, mood string
	db.QueryRow(`SELECT style_config->>'film_reference',style_config->>'mood' FROM projects WHERE id<>'p' LIMIT 1`).Scan(&film, &mood)
	if film != "stored film" || mood != "stored mood" {
		t.Fatal("preferences lost")
	}
}

func TestScriptRequestDisablesThinkingModelCoT(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW',total_seconds=5,keyvisual_url=NULL`)
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []interface{}{
			map[string]interface{}{"message": map[string]string{"content": syntheticScript}},
		}})
	}))
	defer srv.Close()
	t.Setenv("LIMENG_BASE_URL", srv.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	if _, _, err := s.GenerateScript("p", nil); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "qwen3.8-27b-local" {
		t.Fatalf("model = %v", got["model"])
	}
	kwargs, ok := got["chat_template_kwargs"].(map[string]interface{})
	if !ok || kwargs["enable_thinking"] != false {
		t.Fatalf("thinking was not disabled: %#v", got["chat_template_kwargs"])
	}
}
