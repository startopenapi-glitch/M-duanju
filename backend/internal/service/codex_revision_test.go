package service

import (
	"context"
	"drama-platform/internal/model"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCodexRevisionTopupProducesNewVersionAndReusesOtherShot(t *testing.T) {
	ff := testEncoder(t)
	t.Setenv("FFMPEG_PATH", ff)
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(ff), "ffprobe"))
	storage := t.TempDir()
	t.Setenv("STORAGE_DIR", storage)
	payload := syntheticVideo(t)
	var chat, posts atomic.Int32
	var videoPrompt string
	var base string
	mux := http.NewServeMux()
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(storage))))
	mux.HandleFunc("/old.mp4", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/new.mp4", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		chat.Add(1)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), "微笑") {
			t.Error("revision instruction missing from planner")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"changes":[{"seq":2,"visualPrompt":"结尾两人相视微笑","dialogue":"我们回家吧"}]}`}}}})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		videoPrompt, _ = b["prompt"].(string)
		json.NewEncoder(w).Encode(map[string]string{"id": "revision-video", "status": "queued"})
	})
	mux.HandleFunc("/videos/revision-video", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "revision-video", "status": "succeeded", "url": base + "/new.mp4", "charge_micros": 400000, "duration": 5, "resolution": "720P"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	base = server.URL
	t.Setenv("LIMENG_BASE_URL", base)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	t.Setenv("PUBLIC_BASE_URL", base)
	svc, db, oldRun := reviewOrchestratorFixture(t, "COMPLETED")
	if _, err := db.Exec(`UPDATE generation_runs SET status='completed' WHERE id=$1;`, oldRun); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE projects SET final_url=$1,final_run_id=$2,keyvisual_url='synthetic.png',total_seconds=10 WHERE id='review-p'`, base+"/old.mp4", oldRun); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE users SET credits=0 WHERE id='review-u';INSERT INTO users(id,email,password_hash,role) VALUES('admin','admin@example.invalid','x','admin')`)
	for i := 1; i <= 2; i++ {
		if _, err := db.Exec(`INSERT INTO shots(id,project_id,shot_code,seq,status,duration,visual_prompt,video_url,upstream_id,delivered_upstream_id) VALUES($1,'review-p',$1,$2,'APPROVED',5,'旧结尾',$3,$1,$1)`, []string{"", "old1", "old2"}[i], i, base+"/old.mp4"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,status,phase,upstream_id,run_id) VALUES('kv-old','keyvisual','review-p',NULL,'done','done','kv-task',$1),('job-old1','shot','review-p','old1','done','done','old1',$1),('job-old2','shot','review-p','old2','done','done','old2',$1);
`, oldRun); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO generation_artifacts(run_id,job_id,kind,target_id,upstream_id,result_url) VALUES($1,'kv-old','image','','kv-task','synthetic.png'),($1,'job-old1','video','old1','old1',$2),($1,'job-old2','video','old2','old2',$2)`, oldRun, base+"/old.mp4"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE generation_artifacts SET metadata=jsonb_build_object('Kind',kind,'TaskID',upstream_id,'ProjectID','review-p','ShotID',target_id,'URL',result_url,'Duration',5,'Width',1344,'Height',768,'ChargeMicros',400000) WHERE kind='video'`)
	if _, err := db.Exec(`UPDATE shots SET retry_count=$1 WHERE id='old2'`, ShotMaxRetry); err != nil {
		t.Fatal(err)
	}
	req := &StartRunReq{IdempotencyKey: "codex-revise", Mode: "revise", Scope: json.RawMessage(`{"instruction":"结尾改成两个人相视微笑"}`)}
	start, err := svc.StartRun("review-u", "review-p", req)
	if err != nil {
		t.Fatal(err)
	}
	if !start.WaitingForCredits || start.EstimatedCredits != 200 {
		t.Fatalf("only changed 5s shot should require original rate: %#v", start)
	}
	again, err := svc.StartRun("review-u", "review-p", req)
	if err != nil || again.Run.ID != start.Run.ID {
		t.Fatalf("duplicate: %v %#v", err, again)
	}
	if chat.Load() != 1 {
		t.Fatalf("duplicate replanned: %d", chat.Load())
	}
	o, err := svc.CreateOrder("review-u", &model.CreateOrderReq{AmountYuan: 10, RunID: start.Run.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "local fixture"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		svc.ResumeFromWakeups(context.Background(), 10)
		svc.AdvanceActiveRuns(context.Background(), 10)
		for _, kind := range []string{JobShot, JobAssemble} {
			js, e := svc.ClaimJobs(kind, 10)
			if e != nil {
				t.Fatal(e)
			}
			for _, j := range js {
				if e = svc.ExecuteJob(j); e != nil {
					t.Fatal(e)
				}
			}
		}
		mustExec(t, db, `UPDATE jobs SET run_after=now() WHERE status='queued'`)
		v, e := svc.LatestRun("review-u", "review-p")
		if e != nil {
			t.Fatal(e)
		}
		if v.Run.Status == "completed" {
			break
		}
		if v.Run.Status == "failed" {
			t.Fatalf("revision failed: %#v", v)
		}
	}
	v, err := svc.LatestRun("review-u", "review-p")
	if err != nil {
		t.Fatal(err)
	}
	if v.Run.Status != "completed" || posts.Load() != 1 || !strings.Contains(videoPrompt, "相视微笑") {
		t.Fatalf("revision not delivered: run=%s posts=%d prompt=%s", v.Run.Status, posts.Load(), videoPrompt)
	}
	var kept, url, finalRun string
	var bal int64
	db.QueryRow(`SELECT video_url FROM shots WHERE id='old1'`).Scan(&kept)
	db.QueryRow(`SELECT final_url,final_run_id FROM projects WHERE id='review-p'`).Scan(&url, &finalRun)
	db.QueryRow(`SELECT credits FROM users WHERE id='review-u'`).Scan(&bal)
	if kept != base+"/old.mp4" || url == kept || finalRun != start.Run.ID || bal != 960 {
		t.Fatalf("wrong reuse/delivery/money: kept=%s url=%s run=%s bal=%d", kept, url, finalRun, bal)
	}
	file := filepath.Join(storage, strings.TrimPrefix(url, base+"/files/"))
	if _, err = os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(filepath.Join(filepath.Dir(ff), "ffprobe"), "-v", "error", "-show_streams", "-of", "json", file).Output(); err != nil || !strings.Contains(string(out), `"codec_type": "video"`) {
		t.Fatalf("not playable: %v %s", err, out)
	}
	if n := countRows(t, db, `SELECT count(*) FROM credit_transactions WHERE user_id='review-u' AND kind='spend'`); n != 1 {
		t.Fatalf("double charged: %d", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_artifact_reuses x JOIN generation_artifacts a ON a.id=x.artifact_id WHERE x.run_id=$1 AND a.job_id IN ('kv-old','job-old1')`, start.Run.ID); n != 2 {
		t.Errorf("unchanged assets missing explicit provenance: %d", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM generation_artifact_reuses x JOIN generation_artifacts a ON a.id=x.artifact_id WHERE x.run_id=$1 AND a.job_id='job-old2'`, start.Run.ID); n != 0 {
		t.Errorf("changed shot falsely marked reused: %d", n)
	}
	if v.Run.CreditsSettled != 40 {
		t.Errorf("run receipt display=%d want40", v.Run.CreditsSettled)
	}
	data, _ := json.Marshal(v)
	if !strings.Contains(string(data), base+"/old.mp4") || !strings.Contains(string(data), `"outputs"`) {
		t.Errorf("delivered V1 missing from authorized read API: %s", data)
	}
	if _, err = svc.LatestRun("admin", "review-p"); err == nil {
		t.Error("history exposed to another account")
	}
	var saved string
	if err = db.QueryRow(`SELECT base_snapshot::text FROM generation_revision_plans WHERE run_id=$1`, start.Run.ID).Scan(&saved); err != nil || !strings.Contains(saved, "old.mp4") {
		t.Fatalf("old version not retained: %v", err)
	}
}

func TestCodexRevisionRejectsInvalidPlanWithoutChargeableRun(t *testing.T) {
	for _, tt := range []struct {
		name, reply string
		mutate      bool
	}{
		{"unsupported", `{"changes":[],"unsupported":"outside current cast"}`, false},
		{"unknown shot", `{"changes":[{"seq":99,"visualPrompt":"invalid"}]}`, false},
		{"changed while planning", `{"changes":[{"seq":1,"visualPrompt":"new ending"}]}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, db, old := reviewOrchestratorFixture(t, "COMPLETED")
			if _, e := db.Exec(`UPDATE generation_runs SET status='completed' WHERE id=$1`, old); e != nil {
				t.Fatal(e)
			}
			mustExec(t, db, `UPDATE users SET credits=0 WHERE id='review-u';UPDATE projects SET final_url='v1.mp4',keyvisual_url='kv.png' WHERE id='review-p';INSERT INTO shots(id,project_id,seq,shot_code,duration,status,video_url,visual_prompt) VALUES('s','review-p',1,'S01',5,'GENERATED','v1-shot.mp4','old ending')`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.mutate {
					mustExec(t, db, `UPDATE shots SET visual_prompt='concurrent edit' WHERE id='s'`)
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": tt.reply}}}})
			}))
			defer server.Close()
			t.Setenv("LIMENG_BASE_URL", server.URL)
			_, err := svc.StartRun("review-u", "review-p", &StartRunReq{IdempotencyKey: "invalid-plan", Mode: "revise", Scope: json.RawMessage(`{"instruction":"修改结尾"}`)})
			if err == nil {
				t.Fatal("invalid or stale revision accepted")
			}
			if n := countRows(t, db, `SELECT count(*) FROM generation_runs WHERE mode='revise'`); n != 0 {
				t.Fatal("orphan rechargeable run", n)
			}
			if n := countRows(t, db, `SELECT count(*) FROM generation_revision_plans`); n != 0 {
				t.Fatal("orphan revision plan", n)
			}
			if n := countRows(t, db, `SELECT count(*) FROM jobs`); n != 0 {
				t.Fatal("unexpected paid job", n)
			}
			if tt.mutate {
				var text string
				if e := db.QueryRow(`SELECT visual_prompt FROM shots WHERE id='s'`).Scan(&text); e != nil || text != "concurrent edit" {
					t.Fatalf("concurrent edit overwritten: %s %v", text, e)
				}
			}
		})
	}
}
