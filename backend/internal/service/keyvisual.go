package service

// 参考图压缩
//
// 背景：上游（力萌）需要主动拉取我们提供的参考图 URL。
// 本机出口带宽有限（实测约 65 KB/s），一张 1024×1024 PNG 约 930KB，
// 下载要 14 秒，超过上游读取超时 → 报
// "reference image unavailable: context deadline exceeded"。
//
// 对策：参考图只用于视觉锚定，不需要无损。压成 JPEG 后约 60KB，
// 下载约 1 秒，稳定在上游超时阈值内。

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// 上游拉取参考图的目标体积上界（字节）。超过就压缩。
const keyvisualMaxBytes = 220 * 1024

// normalizeKeyVisualAspect crops the provider's square image to the project frame.
// The upstream image model currently returns 1024x1024 regardless of the ratio
// parameter; passing a square reference into the video model is what makes 9:16
// projects look like landscape content with side bars.
func (s *Service) normalizeKeyVisualAspect(srcPath, aspect string) (string, error) {
	var w, h int
	switch aspect {
	case "9:16":
		w, h = 576, 1024
	case "16:9":
		w, h = 1024, 576
	default:
		return srcPath, nil
	}
	f, err := os.CreateTemp(filepath.Dir(srcPath), ".keyvisual-aspect-*.png")
	if err != nil {
		return "", err
	}
	out := f.Name()
	f.Close()
	keep := false
	defer func() {
		if !keep {
			os.Remove(out)
		}
	}()
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", w, h, w, h)
	args := []string{"-y", "-v", "error", "-i", srcPath, "-vf", vf, "-frames:v", "1", out}
	if raw, e := exec.Command(getenvDefault("FFMPEG_PATH", "ffmpeg"), args...).CombinedOutput(); e != nil {
		return "", fmt.Errorf("normalize keyvisual aspect: %w %s", e, truncate(string(raw), 200))
	}
	if err = validateGenerationMedia(out, "image"); err != nil {
		return "", err
	}
	keep = true
	return out, nil
}

// normalizeVideoAspect re-encodes a provider shot into the project's canonical
// frame. H3 currently returns 1312x736 even for a 9:16 project; leaving that raw
// file means previews, extracted frames and the final deliverable disagree.
func (s *Service) normalizeVideoAspect(srcPath, aspect string) (string, error) {
	if aspect != "9:16" && aspect != "16:9" {
		return srcPath, nil
	}
	w, h := targetVideoDimensions(aspect)
	f, err := os.CreateTemp(filepath.Dir(srcPath), ".video-aspect-*.mp4")
	if err != nil {
		return "", err
	}
	out := f.Name()
	f.Close()
	keep := false
	defer func() {
		if !keep {
			os.Remove(out)
		}
	}()
	ffmpeg := getenvDefault("ASSEMBLY_FFMPEG_PATH", getenvDefault("FFMPEG_PATH", "ffmpeg"))
	enc, encArgs := pickEncoder(ffmpeg)
	if enc == "" {
		// Keep delivery possible on hosts without an H.264 encoder. Assembly
		// still enforces the canonical frame; this is a degraded raw preview.
		log.Printf("[video] 无 H.264 编码器，保留上游原始画幅")
		return srcPath, nil
	}
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1", w, h, w, h)
	args := []string{"-y", "-v", "error", "-i", srcPath, "-vf", vf, "-threads", "2"}
	args = append(args, encArgs...)
	args = append(args, "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", out)
	if raw, e := exec.Command(ffmpeg, args...).CombinedOutput(); e != nil {
		log.Printf("[video] 画幅重编码失败，保留原始画幅: %v %s", e, truncate(string(raw), 160))
		return srcPath, nil
	}
	if err = validateGenerationMedia(out, "video"); err != nil {
		log.Printf("[video] 重编码结果校验失败，保留原始画幅: %v", err)
		return srcPath, nil
	}
	keep = true
	return out, nil
}

// PrepareKeyVisualForUpstream 确保参考图适合上游拉取
// 返回最终应对外暴露的本地路径
func (s *Service) PrepareKeyVisualForUpstream(srcPath string) string {
	path, err := s.prepareKeyVisualForUpstream(srcPath)
	if err != nil {
		log.Printf("[keyvisual] 压缩失败，保留原图: %v", err)
		return srcPath
	}
	return path
}

func (s *Service) prepareKeyVisualForUpstream(srcPath string) (string, error) {
	fi, err := os.Stat(srcPath)
	if err != nil {
		return "", err
	}
	if err = validateGenerationMedia(srcPath, "image"); err != nil {
		return "", err
	}
	if fi.Size() <= keyvisualMaxBytes && strings.HasSuffix(strings.ToLower(srcPath), ".jpg") {
		return srcPath, nil
	}
	// ffmpeg can overwrite only its own unique temporary output, never a served JPG.
	f, err := os.CreateTemp(filepath.Dir(srcPath), ".keyvisual-*.jpg")
	if err != nil {
		return "", err
	}
	temporary := f.Name()
	f.Close()
	defer os.Remove(temporary)
	args := []string{"-y", "-v", "error", "-i", srcPath, "-vf", "scale='min(1024,iw)':-2", "-q:v", "3", temporary}
	if out, e := exec.Command(getenvDefault("FFMPEG_PATH", "ffmpeg"), args...).CombinedOutput(); e != nil {
		return "", fmt.Errorf("convert keyvisual: %w %s", e, truncate(string(out), 200))
	}
	if err = validateGenerationMedia(temporary, "image"); err != nil {
		return "", err
	}
	converted, err := os.Stat(temporary)
	if err != nil {
		return "", err
	}
	if converted.Size() >= fi.Size() {
		return srcPath, nil
	}
	digest, err := mediaDigest(temporary)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(filepath.Dir(srcPath), "keyvisual-"+digest+".jpg")
	if err = publishImmutableMedia(temporary, destination); err != nil {
		return "", err
	}
	log.Printf("[keyvisual] 参考图压缩 %.0fKB → %.0fKB", float64(fi.Size())/1024, float64(converted.Size())/1024)
	return destination, nil
}

// RecompressExistingKeyVisuals 批量整理已有的参考图（运维脚本用）
func (s *Service) RecompressExistingKeyVisuals() (int, error) {
	dir := s.storageDir("keyvisuals")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		fi, err := os.Stat(full)
		if err != nil || fi.Size() <= keyvisualMaxBytes {
			continue
		}
		res := s.PrepareKeyVisualForUpstream(full)
		if res != full {
			n++
		}
	}
	return n, nil
}

// DescribeKeyVisualSize 诊断用：报告文件与拉取耗时估算
func DescribeKeyVisualSize(path string, bytesPerSec float64) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "文件不存在"
	}
	sz := float64(fi.Size())
	eta := sz / bytesPerSec
	return fmt.Sprintf("%.0f KB，按 %.0f KB/s 估算约 %.1f 秒",
		sz/1024, bytesPerSec/1024, eta)
}
