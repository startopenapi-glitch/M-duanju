package service

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func syntheticVideo(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/settlement-synthetic.mp4")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func syntheticImage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	m := image.NewRGBA(image.Rect(0, 0, 16, 16))
	m.Set(0, 0, color.RGBA{R: 255, A: 255})
	if e := png.Encode(&b, m); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func deliveredVideo(t *testing.T, s *Service, base, root string) (string, []byte) {
	t.Helper()
	var u string
	if e := s.db.QueryRow(`SELECT video_url FROM shots WHERE id='s'`).Scan(&u); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(root, strings.TrimPrefix(u, base+"/files/")))
	if e != nil {
		t.Fatal(e)
	}
	return u, b
}
func TestGenerationMediaRejectsEmptyAndCorrupt(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("corrupt chunked video")} {
		t.Run(fmt.Sprintf("bytes_%d", len(body)), func(t *testing.T) {
			s, db, _ := settlementFixture(t)
			root := filepath.Join(t.TempDir(), "storage")
			t.Setenv("STORAGE_DIR", root)
			t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
			old := filepath.Join(root, "videos", "old.mp4")
			os.MkdirAll(filepath.Dir(old), 0755)
			os.WriteFile(old, []byte("old-delivery"), 0600)
			mustExec(t, db, `UPDATE shots SET video_url='http://127.0.0.1/files/videos/old.mp4',delivered_upstream_id='old-task' WHERE id='s'`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				w.Write(body)
			}))
			defer server.Close()
			task := &VideoTaskResp{ID: "task-1", Status: "succeeded", URL: server.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
			if e := s.onShotSucceeded("p", "s", "S01", task); e == nil {
				t.Fatal("invalid media accepted")
			}
			b, e := os.ReadFile(old)
			if e != nil || string(b) != "old-delivery" {
				t.Fatalf("old file damaged: %q %v", b, e)
			}
			var url, taskID string
			if e := db.QueryRow(`SELECT video_url,upstream_id FROM shots WHERE id='s'`).Scan(&url, &taskID); e != nil {
				t.Fatal(e)
			}
			if url != "http://127.0.0.1/files/videos/old.mp4" || taskID != "task-1" {
				t.Fatal("old reference or recoverable task lost")
			}
			assertSettlementTotals(t, db, 1000, 0, 0)
		})
	}
}
func TestGenerationMediaConcurrentCallbacksKeepPublishedBytes(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprintf("different_%v", different), func(t *testing.T) {
			s, db, _ := settlementFixture(t)
			root := filepath.Join(t.TempDir(), "storage")
			t.Setenv("STORAGE_DIR", root)
			t.Setenv("FFMPEG_PATH", "/bin/false")
			first := syntheticVideo(t)
			second := append(append([]byte{}, first...), []byte("variant")...)
			ready, release := make(chan struct{}, 2), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ready <- struct{}{}
				<-release
				if r.URL.Path == "/other" {
					w.Write(second)
				} else {
					w.Write(first)
				}
			}))
			defer server.Close()
			t.Setenv("PUBLIC_BASE_URL", server.URL)
			a := VideoTaskResp{ID: "task-1", Status: "succeeded", URL: server.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
			b := a
			if different {
				b.URL += "/other"
			}
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for _, task := range []*VideoTaskResp{&a, &b} {
				wg.Add(1)
				go func(v *VideoTaskResp) { defer wg.Done(); errs <- s.onShotSucceeded("p", "s", "S01", v) }(task)
			}
			<-ready
			<-ready
			close(release)
			wg.Wait()
			close(errs)
			conflicts := 0
			for e := range errs {
				if e != nil {
					conflicts++
				}
			}
			expected := 0
			if different {
				expected = 1
			}
			if conflicts != expected {
				t.Fatalf("conflicts=%d want %d", conflicts, expected)
			}
			beforeURL, before := deliveredVideo(t, s, server.URL, root)
			if !bytes.Equal(before, first) && !bytes.Equal(before, second) {
				t.Fatal("published bytes corrupted")
			}
			bad := a
			bad.ChargeMicros++
			if e := s.onShotSucceeded("p", "s", "S01", &bad); e == nil {
				t.Fatal("conflicting replay accepted")
			}
			afterURL, after := deliveredVideo(t, s, server.URL, root)
			if beforeURL != afterURL || !bytes.Equal(before, after) {
				t.Fatal("rejected replay overwrote delivery")
			}
			assertSettlementTotals(t, db, 800, 200, 1)
		})
	}
}
func TestImagePollingIdentityAndConversionBoundaries(t *testing.T) {
	for _, mode := range []string{"id", "task_id", "id_with_other_task_id", "missing", "wrong_id", "wrong_task_id", "changed_attempt", "conversion_failure"} {
		t.Run(mode, func(t *testing.T) {
			s, db, _ := settlementFixture(t)
			mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='image-1',keyvisual_url='old.jpg' WHERE id='p'`)
			var downloads int32
			var endpoint string
			body := syntheticImage(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/images/") {
					identity := `"id":"image-1",`
					switch mode {
					case "task_id":
						identity = `"task_id":"image-1",`
					case "id_with_other_task_id":
						identity = `"id":"image-1","task_id":"other",`
					case "missing":
						identity = ""
					case "wrong_id":
						identity = `"id":"other",`
					case "wrong_task_id":
						identity = `"task_id":"other",`
					case "changed_attempt":
						mustExec(t, db, `UPDATE projects SET keyvisual_task_id='new-image' WHERE id='p'`)
					}
					fmt.Fprintf(w, `{%s"status":"succeeded","url":%q,"charge_micros":123}`, identity, endpoint+"/asset")
					return
				}
				atomic.AddInt32(&downloads, 1)
				w.Write(body)
			}))
			defer server.Close()
			endpoint = server.URL
			root := filepath.Join(t.TempDir(), "storage")
			t.Setenv("LIMENG_BASE_URL", server.URL)
			t.Setenv("LIMENG_API_KEY", "synthetic")
			t.Setenv("STORAGE_DIR", root)
			t.Setenv("PUBLIC_BASE_URL", server.URL)
			if mode == "conversion_failure" {
				t.Setenv("FFMPEG_PATH", "/bin/false")
			}
			e := s.waitKeyVisual("p", "u", "image-1")
			succeeds := mode == "id" || mode == "task_id" || mode == "id_with_other_task_id" || mode == "missing"
			if succeeds {
				if e != nil {
					t.Fatal(e)
				}
				assertSettlementTotals(t, db, 970, 30, 1)
			} else {
				if e == nil {
					t.Fatal("invalid identity/attempt/conversion accepted")
				}
				assertSettlementTotals(t, db, 1000, 0, 0)
				var task, url string
				if q := db.QueryRow(`SELECT keyvisual_task_id,keyvisual_url FROM projects WHERE id='p'`).Scan(&task, &url); q != nil {
					t.Fatal(q)
				}
				expected := "image-1"
				if mode == "changed_attempt" {
					expected = "new-image"
				}
				if task != expected || url != "old.jpg" {
					t.Fatalf("original image identity/delivery lost: %s %s", task, url)
				}
				if (mode == "wrong_id" || mode == "wrong_task_id" || mode == "changed_attempt") && downloads != 0 {
					t.Fatal("invalid identity downloaded media")
				}
			}
		})
	}
}
func TestKeyvisualConversionCannotOverwriteExistingJPEG(t *testing.T) {
	s, _, _ := settlementFixture(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "image.png")
	old := filepath.Join(dir, "image.jpg")
	os.WriteFile(src, syntheticImage(t), 0600)
	os.WriteFile(old, []byte("previous-jpeg"), 0600)
	s.PrepareKeyVisualForUpstream(src)
	b, e := os.ReadFile(old)
	if e != nil || string(b) != "previous-jpeg" {
		t.Fatalf("conversion overwrote served jpeg: %q %v", b, e)
	}
}

func syntheticLargeImage(t *testing.T, seed uint32) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			seed = 1664525*seed + 1013904223
			m.SetRGBA(x, y, color.RGBA{uint8(seed >> 24), uint8(seed >> 16), uint8(seed >> 8), 255})
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, m); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestImageConcurrentConversionKeepsImmutableDelivery(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprintf("different_%v", different), func(t *testing.T) {
			s, db, _ := settlementFixture(t)
			mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='image-1',keyvisual_url='old.jpg' WHERE id='p'`)
			root := filepath.Join(t.TempDir(), "storage")
			a, b := syntheticLargeImage(t, 1), syntheticLargeImage(t, 2)
			var requests int32
			var endpoint string
			entered, release := make(chan struct{}, 2), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/images/") {
					n := atomic.AddInt32(&requests, 1)
					if n <= 2 {
						entered <- struct{}{}
						<-release
					}
					asset := "/a"
					if different && n == 2 {
						asset = "/b"
					}
					fmt.Fprintf(w, `{"id":"image-1","status":"succeeded","url":%q,"charge_micros":123}`, endpoint+asset)
					return
				}
				if r.URL.Path == "/b" {
					w.Write(b)
				} else {
					w.Write(a)
				}
			}))
			defer server.Close()
			endpoint = server.URL
			t.Setenv("STORAGE_DIR", root)
			t.Setenv("PUBLIC_BASE_URL", server.URL)
			t.Setenv("LIMENG_BASE_URL", server.URL)
			t.Setenv("LIMENG_API_KEY", "synthetic")
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); errs <- s.waitKeyVisual("p", "u", "image-1") }()
			}
			<-entered
			<-entered
			close(release)
			wg.Wait()
			close(errs)
			conflicts := 0
			for e := range errs {
				if e != nil {
					conflicts++
				}
			}
			expected := 0
			if different {
				expected = 1
			}
			if conflicts != expected {
				t.Fatalf("image conflicts=%d want %d", conflicts, expected)
			}
			var url string
			if e := db.QueryRow(`SELECT keyvisual_url FROM projects WHERE id='p'`).Scan(&url); e != nil {
				t.Fatal(e)
			}
			if !strings.HasSuffix(url, ".jpg") {
				t.Fatalf("conversion not exercised: %s", url)
			}
			path := filepath.Join(root, strings.TrimPrefix(url, server.URL+"/files/"))
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			// Conversion failure on a replay must preserve the published JPEG and URL.
			t.Setenv("FFMPEG_PATH", "/bin/false")
			if e := s.waitKeyVisual("p", "u", "image-1"); e == nil {
				t.Fatal("failed conversion accepted")
			}
			after, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("published JPEG changed")
			}
			var afterURL string
			db.QueryRow(`SELECT keyvisual_url FROM projects WHERE id='p'`).Scan(&afterURL)
			if afterURL != url {
				t.Fatal("published image reference changed")
			}
			assertSettlementTotals(t, db, 970, 30, 1)
		})
	}
}
func TestVideoSignedURLRenewalUsesStableArtifactFacts(t *testing.T) {
	s, db, _ := settlementFixture(t)
	body := syntheticVideo(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "storage")
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("PUBLIC_BASE_URL", server.URL)
	t.Setenv("FFMPEG_PATH", "/bin/false")
	task := &VideoTaskResp{ID: "task-1", Status: "succeeded", URL: server.URL + "?signature=one", ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
	if e := s.onShotSucceeded("p", "s", "S01", task); e != nil {
		t.Fatal(e)
	}
	beforeURL, before := deliveredVideo(t, s, server.URL, root)
	task.URL = server.URL + "?signature=renewed"
	if e := s.onShotSucceeded("p", "s", "S01", task); e != nil {
		t.Fatal(e)
	}
	afterURL, after := deliveredVideo(t, s, server.URL, root)
	if beforeURL != afterURL || !bytes.Equal(before, after) {
		t.Fatal("signed URL renewal changed local artifact identity")
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestPollShotsAndPollAllGeneratingSettleOnce(t *testing.T) {
	s, db, _ := settlementFixture(t)
	body := syntheticVideo(t)
	var endpoint string
	entered, release := make(chan struct{}, 2), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/videos/task-1" {
			entered <- struct{}{}
			<-release
			fmt.Fprintf(w, `{"id":"task-1","status":"succeeded","url":%q,"charge_micros":2000000,"duration":5,"resolution":"720P"}`, endpoint+"/asset")
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	endpoint = server.URL
	t.Setenv("LIMENG_BASE_URL", server.URL)
	t.Setenv("LIMENG_API_KEY", "synthetic")
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("PUBLIC_BASE_URL", server.URL)
	t.Setenv("FFMPEG_PATH", "/bin/false")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.PollShots("p") }()
	go func() { defer wg.Done(); s.PollAllGenerating() }()
	wg.Wait()
	if e := s.adoptLegacyProject("p"); e != nil {
		t.Fatal(e)
	}
	var jobs int
	if e := db.QueryRow(`SELECT count(*) FROM jobs WHERE kind='shot'`).Scan(&jobs); e != nil || jobs != 1 {
		t.Fatalf("concurrent poll duplicated adoption: %d %v", jobs, e)
	}
	job := claimOne(t, s, JobShot)
	done := make(chan error, 1)
	go func() { done <- s.ExecuteJob(job) }()
	<-entered
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	s.PollShots("p")
	s.PollAllGenerating()
	assertSettlementTotals(t, db, 800, 200, 1)
}

func TestKnownConflictAndStaleCallbackDoNotDownload(t *testing.T) {
	s, db, _ := settlementFixture(t)
	body := syntheticVideo(t)
	var downloads int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&downloads, 1); w.Write(body) }))
	defer server.Close()
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("PUBLIC_BASE_URL", server.URL)
	t.Setenv("FFMPEG_PATH", "/bin/false")
	task := VideoTaskResp{ID: "task-1", Status: "succeeded", URL: server.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
	if e := s.onShotSucceeded("p", "s", "S01", &task); e != nil {
		t.Fatal(e)
	}
	changed := task
	changed.ChargeMicros++
	if e := s.onShotSucceeded("p", "s", "S01", &changed); e == nil {
		t.Fatal("conflict accepted")
	}
	mustExec(t, db, `UPDATE shots SET status='QUEUED',upstream_id='new-task' WHERE id='s'`)
	stale := task
	stale.ID = "unknown-old-task"
	if e := s.onShotSucceeded("p", "s", "S01", &stale); e == nil {
		t.Fatal("stale callback accepted")
	}
	if downloads != 1 {
		t.Fatalf("known conflicting/stale callback downloaded %d times", downloads)
	}
}

func TestPendingVideoFingerprintConflictPreservesFacts(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE users SET credits=100 WHERE id='u'`)
	var body atomic.Value
	body.Store(syntheticVideo(t))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body.Load().([]byte)) }))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "storage")
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("PUBLIC_BASE_URL", server.URL)
	t.Setenv("FFMPEG_PATH", "/bin/false")
	task := VideoTaskResp{ID: "task-1", Status: "succeeded", URL: server.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
	if e := s.onShotSucceeded("p", "s", "S01", &task); e == nil {
		t.Fatal("expected balance pending")
	}
	var first string
	if e := db.QueryRow(`SELECT result_url FROM generation_settlements WHERE upstream_id='task-1' AND status='pending'`).Scan(&first); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, strings.TrimPrefix(first, server.URL+"/files/"))
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE users SET credits=1000 WHERE id='u'`)
	body.Store(append(append([]byte{}, syntheticVideo(t)...), []byte("different-fingerprint")...))
	if e := s.onShotSucceeded("p", "s", "S01", &task); e == nil {
		t.Fatal("changed pending artifact accepted")
	}
	var afterURL string
	if e := db.QueryRow(`SELECT result_url FROM generation_settlements WHERE upstream_id='task-1'`).Scan(&afterURL); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(path)
	if e != nil || afterURL != first || !bytes.Equal(before, after) {
		t.Fatal("pending artifact facts/bytes changed")
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
	body.Store(syntheticVideo(t))
	if e := s.onShotSucceeded("p", "s", "S01", &task); e != nil {
		t.Fatal(e)
	}
	assertSettlementTotals(t, db, 800, 200, 1)
}
func TestAttemptChangesDuringDownloadPreserveOldDelivery(t *testing.T) {
	s, db, _ := settlementFixture(t)
	root := filepath.Join(t.TempDir(), "storage")
	t.Setenv("STORAGE_DIR", root)
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	t.Setenv("FFMPEG_PATH", "/bin/false")
	old := filepath.Join(root, "videos", "old.mp4")
	os.MkdirAll(filepath.Dir(old), 0755)
	os.WriteFile(old, []byte("old-delivery"), 0600)
	mustExec(t, db, `UPDATE shots SET video_url='http://127.0.0.1/files/videos/old.mp4',delivered_upstream_id='old-task' WHERE id='s'`)
	body := syntheticVideo(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.Write(body) }))
	defer server.Close()
	task := VideoTaskResp{ID: "task-1", Status: "succeeded", URL: server.URL, ChargeMicros: 2000000, Duration: 5, Resolution: "720P"}
	finished := make(chan error, 1)
	go func() { finished <- s.onShotSucceeded("p", "s", "S01", &task) }()
	<-entered
	mustExec(t, db, `UPDATE shots SET upstream_id='new-task' WHERE id='s'`)
	close(release)
	if e := <-finished; e == nil {
		t.Fatal("stale staged delivery accepted")
	}
	after, e := os.ReadFile(old)
	if e != nil || string(after) != "old-delivery" {
		t.Fatal("old file changed")
	}
	var current, url string
	if e := db.QueryRow(`SELECT upstream_id,video_url FROM shots WHERE id='s'`).Scan(&current, &url); e != nil {
		t.Fatal(e)
	}
	if current != "new-task" || url != "http://127.0.0.1/files/videos/old.mp4" {
		t.Fatal("new attempt/old delivery overwritten")
	}
	assertSettlementTotals(t, db, 1000, 0, 0)
}
