package service

import (
	"database/sql"
	"drama-platform/internal/model"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFinalReviewScriptConcurrentEdit(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW',total_seconds=5,keyvisual_url=NULL`)
	token, e := s.claimScript("p")
	if e != nil {
		t.Fatal(e)
	}
	editErr := s.UpdateProject("p", &model.UpdateProjectReq{Title: "user edited title", StyleConfig: json.RawMessage(`{"mood":"user edited mood"}`), BudgetYuan: 9})
	shotErr := s.UpdateShot("p", "s", &model.UpdateShotReq{VisualPrompt: "user edited shot"})
	script, e := parseScriptJSON(syntheticScript)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.saveScript("p", token, script); e != nil {
		t.Fatal(e)
	}
	var title, style string
	var budget, n int
	if e = db.QueryRow(`SELECT title,style_config::text,budget_credits FROM projects WHERE id='p'`).Scan(&title, &style, &budget); e != nil {
		t.Fatal(e)
	}
	db.QueryRow(`SELECT count(*) FROM shots WHERE id='s' AND visual_prompt='user edited shot'`).Scan(&n)
	t.Logf("editProject=%v editShot=%v title=%q style=%s budget=%d preservedEditedShot=%d", editErr, shotErr, title, style, budget, n)
	if editErr == nil && title != "user edited title" || shotErr == nil && n == 0 {
		t.Fatal("accepted edits silently overwritten by active script result")
	}
}
func TestFinalReviewPendingReceiptProviderUnavailable(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE users SET credits=2000`)
	body := syntheticVideo(t)
	var base string
	var expired atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expired.Load() {
			http.Error(w, "expired upstream result", 404)
			return
		}
		if r.Method == "POST" {
			fmt.Fprint(w, `{"id":"pending-local-result"}`)
			return
		}
		if r.URL.Path == "/videos/pending-local-result" {
			fmt.Fprintf(w, `{"status":"succeeded","url":%q,"charge_micros":12000000,"duration":5,"resolution":"720P"}`, base+"/asset")
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	base = srv.URL
	t.Setenv("LIMENG_BASE_URL", base)
	t.Setenv("PUBLIC_BASE_URL", base)
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("FFMPEG_PATH", "/bin/false")
	if _, e := s.GenerateShots("p", "u", nil); e != nil {
		t.Fatal(e)
	}
	j := claimOne(t, s, JobShot)
	if e := s.ExecuteJob(j); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now()`)
	if e := s.ExecuteJob(claimOne(t, s, JobShot)); e == nil {
		t.Fatal("expected budget pause")
	}
	var receiptURL string
	if e := db.QueryRow(`SELECT result_url FROM generation_settlements WHERE upstream_id='pending-local-result' AND status='pending'`).Scan(&receiptURL); e != nil {
		t.Fatal(e)
	}
	// 预算由服务端按授权范围维护；这里直接模拟一次服务端补足。
	mustExec(t, db, `UPDATE projects SET budget_credits=2000 WHERE id='p'`)
	expired.Store(true)
	if e := s.ResumeJob("p", j.ID); e != nil {
		t.Fatal(e)
	}
	err := s.ExecuteJob(claimOne(t, s, JobShot))
	var status string
	db.QueryRow(`SELECT status FROM jobs WHERE id=$1`, j.ID).Scan(&status)
	t.Logf("persisted artifact=%s resume error=%v job status=%s", receiptURL, err, status)
	if err != nil {
		t.Fatal("trusted pending receipt could not settle after provider result expired")
	}
}
func TestFinalReviewImageAssemblyConflict(t *testing.T) {
	s, db := queueFixture(t)
	video := syntheticVideo(t)
	image := syntheticImage(t)
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fmt.Fprint(w, `{"id":"image-during-final"}`)
			return
		}
		if r.URL.Path == "/images/image-during-final" {
			fmt.Fprintf(w, `{"status":"succeeded","url":%q,"charge_micros":300000}`, base+"/image")
			return
		}
		if r.URL.Path == "/image" {
			w.Write(image)
			return
		}
		w.Write(video)
	}))
	defer srv.Close()
	base = srv.URL
	t.Setenv("LIMENG_BASE_URL", base)
	t.Setenv("PUBLIC_BASE_URL", base)
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("ASSEMBLY_FFMPEG_PATH", testEncoder(t))
	t.Setenv("ASSEMBLY_FFPROBE_PATH", filepath.Join(filepath.Dir(testEncoder(t)), "ffprobe"))
	if _, e := db.Exec(`UPDATE shots SET status='APPROVED',duration=1,video_url=$1`, base+"/video"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.EnqueueJob(JobKeyVisual, "p", "", 0, ""); e != nil {
		t.Log("safely blocked image", e)
		return
	}
	if e := s.ExecuteJob(claimOne(t, s, JobKeyVisual)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.EnqueueJob(JobAssemble, "p", "", 0, ""); e != nil {
		t.Log("safely blocked assembly", e)
		return
	}
	if e := s.ExecuteJob(claimOne(t, s, JobAssemble)); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE jobs SET run_after=now() WHERE kind='keyvisual'`)
	e := s.ExecuteJob(claimOne(t, s, JobKeyVisual))
	var status string
	db.QueryRow(`SELECT status FROM projects WHERE id='p'`).Scan(&status)
	t.Logf("paid image settle after accepted assembly: error=%v project=%s", e, status)
	if e != nil {
		t.Fatal("cross-kind status mutation strands paid image")
	}
	_ = os.ErrNotExist
}
func TestFinalReviewPublishedAssemblyMode(t *testing.T) {
	root, e := os.MkdirTemp("/tmp", "drama-final-readable-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	if e = os.Chmod(root, 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("ASSEMBLY_FFMPEG_PATH", testEncoder(t))
	t.Setenv("ASSEMBLY_FFPROBE_PATH", filepath.Join(filepath.Dir(testEncoder(t)), "ffprobe"))
	input := filepath.Join(root, "synthetic.mp4")
	if e := os.WriteFile(input, syntheticVideo(t), 0644); e != nil {
		t.Fatal(e)
	}
	s := &Service{}
	output, e := s.assembleWithFFmpeg([]string{input}, []model.Shot{{Duration: 1}})
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(output)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("published assembly permissions=%04o", info.Mode().Perm())
	if os.Geteuid() == 0 {
		if e := exec.Command("/usr/bin/setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "/usr/bin/head", "-c", "1", output).Run(); e != nil {
			t.Fatal("independent static reader cannot read final", e)
		}
		t.Log("separate UID 65534 can read published final")
	}
	if info.Mode().Perm()&0044 != 0044 {
		t.Fatal("published final is not readable by separate static-serving user")
	}
}

func TestFinalReviewScriptGuardsEveryEditor(t *testing.T) {
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET status='SCRIPT_REVIEW',keyvisual_url=NULL; INSERT INTO characters(id,project_id,code,name) VALUES('c','p','C1','original'); INSERT INTO scenes(id,project_id,code,name) VALUES('sc','p','SC1','original')`)
	if _, e := s.claimScript("p"); e != nil {
		t.Fatal(e)
	}
	for name, edit := range map[string]func() error{
		"character": func() error { return s.UpdateCharacter("p", "c", &model.UpdateCharacterReq{Name: "changed"}) },
		"scene":     func() error { return s.UpdateScene("p", "sc", &model.UpdateSceneReq{Name: "changed"}) },
	} {
		t.Run(name, func(t *testing.T) {
			if e := edit(); e == nil {
				t.Fatal("editor accepted active script mutation")
			}
		})
	}
	// 客户侧预算入口已取消：只有服务端能改上限，内容编辑仍被脚本锁拒绝。
	if e := s.UpdateProject("p", &model.UpdateProjectReq{Title: "changed"}); e == nil {
		t.Fatal("content edit accepted while script generating")
	}
	var budget int
	if e := db.QueryRow(`SELECT budget_credits FROM projects WHERE id='p'`).Scan(&budget); e != nil || budget != 1000 {
		t.Fatal("budget changed by client", budget, e)
	}
}
func TestFinalReviewCrossPhaseQueueMatrix(t *testing.T) {
	for _, status := range []string{"queued", "running", "reconcile"} {
		for _, first := range []string{JobKeyVisual, JobShot, JobAssemble} {
			for _, next := range []string{JobKeyVisual, JobShot, JobAssemble} {
				if first == next {
					continue
				}
				t.Run(status+"/"+first+"/"+next, func(t *testing.T) {
					s, db := queueFixture(t)
					mustExec(t, db, `UPDATE shots SET status='APPROVED',video_url='old.mp4'; INSERT INTO jobs(kind,project_id,status,phase) VALUES('`+first+`','p','`+status+`','ready')`)
					var e error
					if next == JobShot {
						e = s.RegenerateShot("p", 1, false)
					} else {
						_, e = s.EnqueueJob(next, "p", "", 0, "")
					}
					if e == nil {
						t.Fatal("cross-phase work accepted")
					}
				})
			}
		}
	}
}
func TestFinalReviewFramesPublishedForStaticReader(t *testing.T) {
	s, db := queueFixture(t)
	root, e := os.MkdirTemp("/tmp", "drama-publish-permissions-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	if e = os.Chmod(root, 0755); e != nil {
		t.Fatal(e)
	}
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("FFMPEG_PATH", testEncoder(t))
	video := filepath.Join(root, "synthetic.mp4")
	if out, e := exec.Command(testEncoder(t), "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=5", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", video).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	if _, e = db.Exec(`UPDATE shots SET video_url=$1,status='GENERATED'`, s.publicURL(video)); e != nil {
		t.Fatal(e)
	}
	s.extractFrames("p", "s", video)
	var url string
	if e = db.QueryRow(`SELECT path FROM shot_frames LIMIT 1`).Scan(&url); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, strings.TrimPrefix(url, "http://127.0.0.1/files/"))
	info, e := os.Stat(filepath.Dir(path))
	if e != nil || info.Mode().Perm()&0005 != 0005 {
		t.Fatal("published frames directory not traversable", info, e)
	}
	if os.Geteuid() == 0 {
		cmd := exec.Command("/usr/bin/setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "/usr/bin/head", "-c", "1", path)
		if e = cmd.Run(); e != nil {
			t.Fatal("independent static reader cannot read frame", e)
		}
	}
}

func finalPendingFixture(t *testing.T, kind string) (*Service, *sql.DB, *Job, string) {
	t.Helper()
	s, db, in := settlementFixture(t)
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("FFMPEG_PATH", "/bin/false")
	payload := syntheticVideo(t)
	jobKind := JobShot
	shot := "s"
	budget := int64(100)
	if kind == "image" {
		t.Setenv("FFMPEG_PATH", testEncoder(t))
		t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(testEncoder(t)), "ffprobe"))
		payload = syntheticImage(t)
		jobKind = JobKeyVisual
		shot = ""
		budget = 10
		in.Kind = "image"
		in.ShotID = ""
		in.Duration = 0
		in.Width = 0
		in.Height = 0
		mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='task-1'; UPDATE shots SET status='PENDING',upstream_id=NULL`)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	t.Cleanup(server.Close)
	file, e := s.stageGenerationArtifact(server.URL, kind, in.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	in.URL = s.publicURL(file)
	if _, e = db.Exec(`UPDATE projects SET budget_credits=$1`, budget); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SettleGeneration(in); e == nil {
		t.Fatal("expected pending")
	}
	mustExec(t, db, `UPDATE projects SET budget_credits=2000`)
	if _, e = db.Exec(`INSERT INTO jobs(id,kind,project_id,shot_id,seq,upstream_id,status,phase,reserved_credits) VALUES('pending-job',$1,'p',NULLIF($2,''),1,'task-1','queued','polling',200)`, jobKind, shot); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LIMENG_BASE_URL", "http://127.0.0.1:1")
	return s, db, claimOne(t, s, jobKind), file
}
func TestFinalReviewPendingIntegrityAndReplay(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "symlink", "foreign-owner", "stale-lease", "stale-task"} {
		t.Run(mode, func(t *testing.T) {
			s, db, j, file := finalPendingFixture(t, "video")
			switch mode {
			case "missing":
				if e := os.Remove(file); e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				if e := os.WriteFile(file, []byte("corrupt"), 0644); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				real := file + ".real"
				if e := os.Rename(file, real); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(real, file); e != nil {
					t.Fatal(e)
				}
			case "foreign-owner":
				mustExec(t, db, `INSERT INTO users(id,email,password_hash) VALUES('u2','foreign@example.com','unused'); UPDATE generation_settlements SET user_id='u2'`)
			case "stale-lease":
				mustExec(t, db, `UPDATE jobs SET lease_token=lease_token+1`)
			case "stale-task":
				mustExec(t, db, `UPDATE shots SET upstream_id='another-task'`)
			}
			if e := s.ExecuteJob(j); e == nil {
				t.Fatal("invalid pending artifact/identity was settled")
			}
			assertSettlementTotals(t, db, 1000, 0, 0)
			var state string
			if e := db.QueryRow(`SELECT status FROM generation_settlements LIMIT 1`).Scan(&state); e != nil || state != "pending" {
				t.Fatal("pending lost", state, e)
			}
		})
	}
	for _, kind := range []string{"video", "image"} {
		t.Run(kind+"/concurrent-replay", func(t *testing.T) {
			s, db, j, _ := finalPendingFixture(t, kind)
			var wg sync.WaitGroup
			errch := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errch <- s.ExecuteJob(j) }()
			}
			wg.Wait()
			close(errch)
			for e := range errch {
				if e != nil && !errors.Is(e, ErrLeaseLost) {
					t.Error(e)
				}
			}
			cost := int64(200)
			if kind == "image" {
				cost = 30
			}
			assertSettlementTotals(t, db, 1000-cost, cost, 1)
			var state string
			var reserved int
			if e := db.QueryRow(`SELECT status,reserved_credits FROM jobs WHERE id='pending-job'`).Scan(&state, &reserved); e != nil || state != "done" || reserved != 0 {
				t.Fatal(state, reserved, e)
			}
		})
	}
}
