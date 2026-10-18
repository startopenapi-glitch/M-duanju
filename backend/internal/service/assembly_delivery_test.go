package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testEncoder(t *testing.T) string {
	t.Helper()
	path := os.Getenv("DRAMA_TEST_FFMPEG")
	if path == "" {
		t.Fatal("DRAMA_TEST_FFMPEG required for actual synthetic media acceptance")
	}
	return path
}
func TestEncoderProbeUsesEachConfiguredPath(t *testing.T) {
	good := testEncoder(t)
	if n, _ := pickEncoder(good); n != "libx264" {
		t.Fatal("synthetic H264 tool unavailable", n)
	}
	if n, _ := pickEncoder("/bin/false"); n != "" {
		t.Fatal("stale cached encoder accepted invalid new path", n)
	}
}
func TestActualAssemblyMixedAudioDelivery(t *testing.T) {
	ff := testEncoder(t)
	t.Setenv("FFMPEG_PATH", ff)
	t.Setenv("FFPROBE_PATH", filepath.Join(filepath.Dir(ff), "ffprobe"))
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "")
	dir := t.TempDir()
	storage := filepath.Join(dir, "custom-media")
	t.Setenv("STORAGE_DIR", storage)
	t.Setenv("PUBLIC_BASE_URL", "http://127.0.0.1")
	for i, name := range []string{"audio.mp4", "silent.mp4"} {
		args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=1"}
		if i == 0 {
			args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "aac")
		}
		args = append(args, "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-t", "1", filepath.Join(dir, name))
		if out, e := exec.Command(ff, args...).CombinedOutput(); e != nil {
			t.Fatalf("synthetic encoder %v %s", e, out)
		}
	}
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	s, db := queueFixture(t)
	if _, e := db.Exec(`UPDATE shots SET status='APPROVED',video_url=$1,duration=1 WHERE id='s';`, server.URL+"/audio.mp4"); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO shots(id,project_id,shot_code,seq,status,video_url,duration) VALUES('s2','p','S02',2,'APPROVED',$1,1)`, server.URL+"/silent.mp4"); e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, `UPDATE projects SET final_url='old-final.mp4'`)
	if _, e := s.EnqueueJob(JobAssemble, "p", "", 0, ""); e != nil {
		t.Fatal(e)
	}
	job := claimOne(t, s, JobAssemble)
	if e := s.ExecuteJob(job); e != nil {
		t.Fatal(e)
	}
	var url, status, jstatus string
	if e := db.QueryRow(`SELECT p.final_url,p.status,j.status FROM projects p JOIN jobs j ON j.project_id=p.id WHERE j.id=$1`, job.ID).Scan(&url, &status, &jstatus); e != nil {
		t.Fatal(e)
	}
	if status != "COMPLETED" || jstatus != "done" || !strings.Contains(url, "/files/exports/") {
		t.Fatalf("not atomically published: %s %s %s", url, status, jstatus)
	}
	path := filepath.Join(storage, strings.TrimPrefix(url, "http://127.0.0.1/files/"))
	out, e := exec.Command(os.Getenv("FFPROBE_PATH"), "-v", "error", "-show_streams", "-show_format", "-of", "json", path).Output()
	if e != nil {
		t.Fatal(e)
	}
	var probe struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			PixFmt    string `json:"pix_fmt"`
		}
	}
	if e = json.Unmarshal(out, &probe); e != nil {
		t.Fatal(e)
	}
	video, audio := false, false
	for _, st := range probe.Streams {
		if st.CodecType == "video" {
			video = st.CodecName == "h264" && st.PixFmt == "yuv420p"
		}
		if st.CodecType == "audio" {
			audio = st.CodecName == "aac"
		}
	}
	if !video || !audio {
		t.Fatalf("delivery codecs: %s", out)
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Index(data, []byte("moov")) < 0 || bytes.Index(data, []byte("moov")) > bytes.Index(data, []byte("mdat")) {
		t.Fatal("not faststart")
	}
	t.Logf("real mixed-audio assembly: h264/aac/yuv420p, moov before mdat, bytes=%d", len(data))
}
