package service

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"drama-platform/internal/model"
)

func TestTargetVideoDimensions(t *testing.T) {
	t.Setenv("VIDEO_RESOLUTION", "720P")
	if w, h := targetVideoDimensions("16:9"); w != 1280 || h != 720 {
		t.Fatalf("16:9 720P = %dx%d", w, h)
	}
	if w, h := targetVideoDimensions("9:16"); w != 720 || h != 1280 {
		t.Fatalf("9:16 720P = %dx%d", w, h)
	}
	t.Setenv("VIDEO_RESOLUTION", "1080P")
	if w, h := targetVideoDimensions("16:9"); w != 1920 || h != 1080 {
		t.Fatalf("16:9 1080P = %dx%d", w, h)
	}
	if w, h := targetVideoDimensions("9:16"); w != 1080 || h != 1920 {
		t.Fatalf("9:16 1080P = %dx%d", w, h)
	}
}

func TestNormalizeKeyVisualAspect(t *testing.T) {
	src := filepath.Join(t.TempDir(), "square.png")
	if err := os.WriteFile(src, syntheticImage(t), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	for _, tc := range []struct {
		aspect string
		w, h   int
	}{{"16:9", 1024, 576}, {"9:16", 576, 1024}} {
		out, err := s.normalizeKeyVisualAspect(src, tc.aspect)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		os.Remove(out)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != tc.w || cfg.Height != tc.h {
			t.Fatalf("%s = %dx%d, want %dx%d", tc.aspect, cfg.Width, cfg.Height, tc.w, tc.h)
		}
	}
}

func TestAssemblyUsesCanonicalAspect(t *testing.T) {
	t.Setenv("STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	s, db := queueFixture(t)
	mustExec(t, db, `UPDATE projects SET aspect='9:16' WHERE id='p'`)
	input := filepath.Join(t.TempDir(), "synthetic.mp4")
	if err := os.WriteFile(input, syntheticVideo(t), 0644); err != nil {
		t.Fatal(err)
	}
	output, err := s.assembleWithFFmpeg([]string{input}, []model.Shot{{ProjectID: "p", Duration: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(output)
	m, err := probeAssemblyMedia(testEncoder(t), output)
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 720 || m.Height != 1280 {
		t.Fatalf("portrait assembly = %dx%d", m.Width, m.Height)
	}
}

func TestNormalizeVideoAspect(t *testing.T) {
	src := filepath.Join(t.TempDir(), "landscape.mp4")
	if err := os.WriteFile(src, syntheticVideo(t), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	for _, tc := range []struct {
		aspect string
		w, h   int
	}{{"16:9", 1280, 720}, {"9:16", 720, 1280}} {
		out, err := s.normalizeVideoAspect(src, tc.aspect)
		if err != nil {
			t.Fatal(err)
		}
		m, err := probeAssemblyMedia(testEncoder(t), out)
		os.Remove(out)
		if err != nil {
			t.Fatal(err)
		}
		if m.Width != tc.w || m.Height != tc.h {
			t.Fatalf("%s = %dx%d, want %dx%d", tc.aspect, m.Width, m.Height, tc.w, tc.h)
		}
	}
}
