package service

// 从 DRAFT 点击一次开始，经真实服务端调度入口（AdvanceActiveRuns + worker 认领）
// 与本地 mock 模型/图片/视频，直到本地可播放成片。不逐段手动调用 AdvanceRun。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func TestRemediationClickOnceFromDraftToPlayableFinal(t *testing.T) {
	ff := os.Getenv("DRAMA_TEST_FFMPEG")
	if ff == "" {
		t.Skip("DRAMA_TEST_FFMPEG not set")
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "storage")
	if err := os.MkdirAll(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	// 合成媒体：一张定妆照、一段 5 秒带音轨视频。
	kv := filepath.Join(dir, "kv.png")
	if out, err := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi",
		"-i", "color=c=blue:s=512x512:d=1", "-frames:v", "1", kv).CombinedOutput(); err != nil {
		t.Fatalf("kv: %v %s", err, out)
	}
	shot := filepath.Join(dir, "shot.mp4")
	if out, err := exec.Command(ff, "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=320x240:d=5",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5",
		"-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "-t", "5", shot).CombinedOutput(); err != nil {
		t.Fatalf("shot: %v %s", err, out)
	}

	var chat, images, videos atomic.Int32
	var base string
	mux := http.NewServeMux()
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(storage))))
	mux.Handle("/kv.png", http.FileServer(http.Dir(dir)))
	mux.Handle("/shot.mp4", http.FileServer(http.Dir(dir)))
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		chat.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": map[string]string{"content": syntheticScript}},
		}})
	})
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		images.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"id": "auto-img", "status": "queued"})
	})
	mux.HandleFunc("/images/auto-img", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "auto-img", "status": "succeeded", "url": base + "/kv.png", "charge_micros": 0,
		})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		videos.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"id": "auto-vid", "status": "queued"})
	})
	mux.HandleFunc("/videos/auto-vid", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "auto-vid", "status": "succeeded", "url": base + "/shot.mp4",
			"charge_micros": 400000, "duration": 5, "resolution": "720P",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	base = server.URL

	t.Setenv("LIMENG_BASE_URL", server.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	t.Setenv("LIMENG_TEXT_MODEL", "synthetic-text")
	t.Setenv("STORAGE_DIR", storage)
	t.Setenv("PUBLIC_BASE_URL", server.URL)
	t.Setenv("VIDEO_RESOLUTION", "720P")
	t.Setenv("FFMPEG_PATH", ff)
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(ff), "ffprobe"))
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "")

	db := testutil.DB(t)
	svc := New(db, "remediation-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,nickname,credits,status)
		VALUES('auto-u','auto@example.com','x','A',0,'active')`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status,aspect)
		VALUES('auto-p','auto-u','synthetic','0',5,'DRAFT','16:9')`)

	start, err := svc.StartRun("auto-u", "auto-p", &StartRunReq{IdempotencyKey: "auto-click"})
	if err != nil {
		t.Fatal(err)
	}
	runID := start.Run.ID
	if !start.WaitingForCredits || start.Run.Status != "waiting_credits" {
		t.Fatalf("expected waiting_credits before top-up, got waiting=%v status=%s", start.WaitingForCredits, start.Run.Status)
	}
	// 模拟充值入账：只写余额/流水/唤醒，不直接执行生成。
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,nickname,role) VALUES
		('admin','admin@example.com','x','Admin','admin') ON CONFLICT (id) DO NOTHING`)
	order, err := svc.CreateOrder("auto-u", &model.CreateOrderReq{AmountYuan: 100, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleOrder("admin", order.ID, &model.HandleOrderReq{Approve: true, Note: "synthetic top-up"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var runStatus string
	for deadline := time.Now().Add(80 * time.Second); time.Now().Before(deadline); {
		// 真实服务端调度入口：消费充值唤醒 + 推进执行 + worker 认领/执行。
		if _, err := svc.ResumeFromWakeups(ctx, 10); err != nil {
			t.Fatal(err)
		}
		svc.AdvanceActiveRuns(ctx, 10)
		for _, kind := range []string{JobKeyVisual, JobShot, JobAssemble} {
			jobs, err := svc.ClaimJobs(kind, 5)
			if err != nil {
				t.Fatal(err)
			}
			for _, j := range jobs {
				if err := svc.ExecuteJob(j); err != nil {
					t.Logf("job %s/%s: %v", kind, j.ID, err)
				}
			}
		}
		// 测试内不等待 5 秒轮询间隔，直接把排队任务拉回可认领时间。
		mustExec(t, db, `UPDATE jobs SET run_after=now() WHERE status='queued'`)
		if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&runStatus); err != nil {
			t.Fatal(err)
		}
		if runStatus == "completed" {
			break
		}
		if runStatus == "failed" {
			var reason, pstatus string
			db.QueryRow(`SELECT waiting_reason FROM generation_runs WHERE id=$1`, runID).Scan(&reason)
			db.QueryRow(`SELECT status FROM projects WHERE id='auto-p'`).Scan(&pstatus)
			t.Fatalf("run failed reason=%s project=%s", reason, pstatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runStatus != "completed" {
		t.Fatalf("run did not complete: %s", runStatus)
	}

	var finalURL, finalRun string
	if err := db.QueryRow(`SELECT COALESCE(final_url,''),COALESCE(final_run_id,'') FROM projects WHERE id='auto-p'`).
		Scan(&finalURL, &finalRun); err != nil {
		t.Fatal(err)
	}
	if finalRun != runID || finalURL == "" {
		t.Fatalf("final delivery not owned by run: final=%q run=%q want %s", finalURL, finalRun, runID)
	}
	local := filepath.Join(storage, strings.TrimPrefix(finalURL, server.URL+"/files/"))
	data, err := os.ReadFile(local)
	if err != nil || len(data) < 1024 {
		t.Fatalf("final not readable: %v bytes=%d", err, len(data))
	}
	out, err := exec.Command(filepath.Join(filepath.Dir(ff), "ffprobe"), "-v", "error",
		"-show_streams", "-of", "json", local).Output()
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatal(err)
	}
	hasVideo, hasAudio := false, false
	for _, st := range probe.Streams {
		hasVideo = hasVideo || st.CodecType == "video"
		hasAudio = hasAudio || st.CodecType == "audio"
	}
	if !hasVideo || !hasAudio {
		t.Fatalf("final streams incomplete: %s", out)
	}

	if chat.Load() != 1 || images.Load() != 1 || videos.Load() != 1 {
		t.Fatalf("provider submissions chat=%d image=%d video=%d, want 1/1/1", chat.Load(), images.Load(), videos.Load())
	}
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='auto-p' AND run_id=$1`, runID); n != 3 {
		t.Fatalf("run-bound jobs = %d, want 3", n)
	}
	var credits int64
	if err := db.QueryRow(`SELECT credits FROM users WHERE id='auto-u'`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 9930 {
		t.Fatalf("credits = %d, want 9930", credits)
	}
}
