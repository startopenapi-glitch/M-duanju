package service

// 端到端：模拟支付入账 → 唤醒 → 自动推进（定妆照/分镜/合成）→ 可读取成片。
// 上游全部由本地 httptest 模拟，媒体用本机 ffmpeg 生成，不触达真实渠道。

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func e2eMedia(t *testing.T, dir, ff string) (string, string) {
	t.Helper()
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
	return kv, shot
}

func TestFullAutoRunFromPaymentToReadableFinal(t *testing.T) {
	ff := os.Getenv("DRAMA_TEST_FFMPEG")
	if ff == "" {
		t.Skip("DRAMA_TEST_FFMPEG not set")
	}
	dir := t.TempDir()
	storage := filepath.Join(dir, "storage")
	os.MkdirAll(storage, 0o755)
	e2eMedia(t, dir, ff)

	mux := http.NewServeMux()
	var base string
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(storage))))
	mux.Handle("/kv.png", http.FileServer(http.Dir(dir)))
	mux.Handle("/shot.mp4", http.FileServer(http.Dir(dir)))
	mux.HandleFunc("/images", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "img-1", "status": "queued"})
	})
	mux.HandleFunc("/images/img-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "img-1", "status": "succeeded", "url": base + "/kv.png", "charge_micros": 0,
		})
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "vid-1", "status": "queued"})
	})
	mux.HandleFunc("/videos/vid-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "vid-1", "status": "succeeded", "url": base + "/shot.mp4",
			"charge_micros": 400000, "duration": 5, "resolution": "720P",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	base = server.URL

	t.Setenv("LIMENG_BASE_URL", server.URL)
	t.Setenv("STORAGE_DIR", storage)
	t.Setenv("PUBLIC_BASE_URL", server.URL)
	t.Setenv("VIDEO_RESOLUTION", "720P")
	t.Setenv("FFMPEG_PATH", ff)
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(ff), "ffprobe"))
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "")

	db := testutil.DB(t)
	svc := New(db, "synthetic-test-secret")
	mustExec(t, db, `INSERT INTO users(id,email,password_hash,credits,status) VALUES
		('u1','u1@example.com','x',0,'active'),
		('admin','admin@example.com','x',0,'active')`)
	mustExec(t, db, `INSERT INTO projects(id,user_id,idea,budget_credits,total_seconds,status,aspect)
		VALUES('p1','u1','synthetic',100000,5,'SCRIPT_REVIEW','16:9')`)
	mustExec(t, db, `INSERT INTO shots(id,project_id,shot_code,seq,duration,status)
		VALUES('s1','p1','S01',1,5,'PENDING')`)

	start, err := svc.StartRun("u1", "p1", &StartRunReq{IdempotencyKey: "e2e-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !start.WaitingForCredits || start.Run.Status != "waiting_credits" {
		t.Fatalf("初始应等待充值，实际 waiting=%v status=%s", start.WaitingForCredits, start.Run.Status)
	}
	runID := start.Run.ID

	// 模拟支付：人工审核入账，只写持久化唤醒，不直接执行生成。
	o, err := svc.CreateOrder("u1", &model.CreateOrderReq{AmountYuan: 100, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleOrder("admin", o.ID, &model.HandleOrderReq{Approve: true, Note: "e2e"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResumeFromWakeups(context.Background(), 10); err != nil {
		t.Fatal(err)
	}

	// 定妆照 job：第一次认领提交、第二次认领轮询并交付。
	runJobRound(t, svc, db, JobKeyVisual)
	runJobRound(t, svc, db, JobKeyVisual)
	if got := projectStatus(t, db); got != "KEYVISUAL_REVIEW" {
		t.Fatalf("定妆照后项目状态 = %s", got)
	}

	// 自动推进到分镜。
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if got := projectStatus(t, db); got != "SHOTS_GENERATING" {
		t.Fatalf("派发分镜后项目状态 = %s", got)
	}
	runJobRound(t, svc, db, JobShot)
	runJobRound(t, svc, db, JobShot)
	if got := projectStatus(t, db); got != "SHOTS_REVIEW" {
		t.Fatalf("分镜后项目状态 = %s", got)
	}

	// 自动通过并合成。
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	runJobRound(t, svc, db, JobAssemble)
	var finalURL, status string
	if err := db.QueryRow(`SELECT COALESCE(final_url,''),status FROM projects WHERE id='p1'`).Scan(&finalURL, &status); err != nil {
		t.Fatal(err)
	}
	if status != "COMPLETED" || finalURL == "" {
		t.Fatalf("合成未完成: %s %s", status, finalURL)
	}
	local := filepath.Join(storage, strings.TrimPrefix(finalURL, server.URL+"/files/"))
	if !strings.HasPrefix(finalURL, server.URL+"/files/") {
		t.Fatalf("成片 URL 不在存储目录: %s", finalURL)
	}

	// 最终推进只把 run 标记完成，不重复派发。
	if err := svc.AdvanceRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	var runStatus string
	if err := db.QueryRow(`SELECT status FROM generation_runs WHERE id=$1`, runID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "completed" {
		t.Fatalf("run status = %s, want completed", runStatus)
	}

	// 成片必须真实可读：有效容器、视频/音频流，且不是空文件。
	data, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 1024 {
		t.Fatalf("成片过小: %d bytes", len(data))
	}
	out, err := exec.Command(filepath.Join(filepath.Dir(ff), "ffprobe"),
		"-v", "error", "-show_streams", "-of", "json", local).Output()
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
	video, audio := false, false
	for _, st := range probe.Streams {
		video = video || st.CodecType == "video"
		audio = audio || st.CodecType == "audio"
	}
	if !video || !audio {
		t.Fatalf("成片流不完整: %s", out)
	}
	if bytes.Index(data, []byte("moov")) < 0 || bytes.Index(data, []byte("moov")) > bytes.Index(data, []byte("mdat")) {
		t.Fatal("成片不是 faststart")
	}

	// 产物与执行可追溯：三个阶段的 job 都关联到本次 run。
	if n := countRows(t, db, `SELECT count(*) FROM jobs WHERE project_id='p1' AND run_id=$1`, runID); n != 3 {
		t.Fatalf("关联到 run 的 job = %d, want 3", n)
	}

	// 计费：定妆照 30 + 分镜实际 40，余额从 10000 扣到 9930。
	var credits int64
	if err := db.QueryRow(`SELECT credits FROM users WHERE id='u1'`).Scan(&credits); err != nil {
		t.Fatal(err)
	}
	if credits != 9930 {
		t.Fatalf("credits = %d, want 9930", credits)
	}
}

func projectStatus(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM projects WHERE id='p1'`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// runJobRound 认领并执行一次指定类型 job；若 job 因提交后等待轮询被顺延，则把 run_after 拉回现在。
func runJobRound(t *testing.T, svc *Service, db *sql.DB, kind string) {
	t.Helper()
	mustExec(t, db, `UPDATE jobs SET run_after=now() WHERE kind='`+kind+`' AND status='queued'`)
	jobs, err := svc.ClaimJobs(kind, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("claim %s: %v %d", kind, err, len(jobs))
	}
	if err := svc.ExecuteJob(jobs[0]); err != nil {
		t.Fatalf("execute %s: %v", kind, err)
	}
}
