package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"drama-platform/internal/model"
)

// ══════════════════ 提示词拼装 ══════════════════

// buildKeyVisualPrompt 定妆照提示词（双人同框，锚定全片）
func buildKeyVisualPrompt(p *model.Project, chars []model.Character, scenes []model.Scene) string {
	var b strings.Builder

	aspect := "横版 16:9"
	if p.Aspect == "9:16" {
		aspect = "竖版 9:16"
	}

	b.WriteString("照片级写实电影剧照，Arri Alexa Mini 质感，35mm 胶片电影质感，")
	b.WriteString(aspect + "，高对比低照度，大面积暗部留白。\n\n")

	if len(scenes) > 0 {
		sc := scenes[0]
		b.WriteString("【场景】\n")
		b.WriteString(sc.Location + "，" + sc.TimeOfDay + "。\n")
		if sc.Lighting != "" {
			b.WriteString(sc.Lighting + "。\n")
		}
		if len(sc.Props) > 0 {
			b.WriteString("场景中可辨认的道具：" + strings.Join(sc.Props, "、") + "。\n")
		}
	}

	b.WriteString("\n【构图】\n")
	if p.Aspect == "9:16" {
		b.WriteString("两人上下分置，竖版中景，相机与视线齐平。\n")
	} else {
		b.WriteString("两人隔一张桌子相对而坐，横版中景，相机位于一侧、略低于视线高度。\n")
	}

	pos := []string{"画面左", "画面右"}
	for i, c := range chars {
		if i >= 2 {
			break
		}
		b.WriteString(fmt.Sprintf("\n【人物%s·%s】\n", pos[i], c.Name))
		if c.AgeRange != "" {
			b.WriteString(c.AgeRange + "，\n")
		}
		b.WriteString(c.Appearance + "。\n")
		if c.Wardrobe != "" {
			b.WriteString(c.Wardrobe + "。\n")
		}
		if c.Persona != "" {
			b.WriteString("人物状态：" + c.Persona + "。\n")
		}
	}

	if len(scenes) > 0 && scenes[0].ColorTemp != "" {
		b.WriteString("\n【色温分区】" + scenes[0].ColorTemp + " —— 两人在色彩上应明确分属两个阵营。")
	}

	return b.String()
}

// buildShotPrompt 分镜提示词
func buildShotPrompt(p *model.Project, chars []model.Character, scenes []model.Scene, sh *model.Shot, aspect string) string {
	var b strings.Builder

	b.WriteString("实拍电影质感，低饱和，高对比低照度，大面积暗部留白。\n")
	if aspect == "9:16" {
		b.WriteString("竖版 9:16 构图，人物居中，适合手机全屏观看。\n")
	} else {
		b.WriteString("横版 16:9 宽银幕构图。\n")
	}

	if len(scenes) > 0 {
		sc := scenes[0]
		b.WriteString("场景：" + sc.Location + "，" + sc.TimeOfDay + "。\n")
		if sc.Lighting != "" {
			b.WriteString("光线：" + sc.Lighting + "。\n")
		}
		if sc.ColorTemp != "" {
			b.WriteString("色温策略：" + sc.ColorTemp + "。\n")
		}
		if len(sc.Props) > 0 {
			b.WriteString("画面中可辨认的道具：" + strings.Join(sc.Props, "、") + "。\n")
		}
	}

	if sh.ShotSize != "" {
		b.WriteString("景别：" + sh.ShotSize + "。\n")
	}
	if sh.CameraHeight != "" {
		b.WriteString("机位：" + sh.CameraHeight + "。\n")
	}
	if sh.CameraMove != "" {
		b.WriteString("运镜：" + sh.CameraMove + "。\n")
	}
	if sh.VisualPrompt != "" {
		b.WriteString(sh.VisualPrompt + "\n")
	}

	for _, c := range chars {
		b.WriteString(fmt.Sprintf("本镜说话人物：%s（%s）——%s；%s。\n",
			c.Name, c.Code, c.Appearance, c.Wardrobe))
	}

	if sh.Dialogue != "" {
		b.WriteString("\n[台词]\n")
		if len(chars) > 0 {
			c := chars[0]
			b.WriteString(fmt.Sprintf("%s（%s），%s，说道：<d>[Chinese] %s</d>\n",
				c.Name, c.Code, c.VoiceProfile, sh.Dialogue))
		} else {
			b.WriteString(fmt.Sprintf("%s 说道：<d>[Chinese] %s</d>\n", sh.SpeakerCode, sh.Dialogue))
		}
	}

	if sh.Soundscape != "" {
		b.WriteString("\noverall_soundscape: " + sh.Soundscape + "\n")
	}
	if sh.MusicCue != "" {
		b.WriteString("non_diegetic_music: " + sh.MusicCue + "\n")
	}

	return b.String()
}

// ══════════════════ ffmpeg 合成 ══════════════════

// Probe each configured binary; a process-wide result could mask a changed path.
// Only H.264 is a supported delivery contract. A hardware listing is not enough.
func pickEncoder(ffmpeg string) (string, []string) {
	candidates := []struct {
		name string
		args []string
	}{
		{"libx264", []string{"-c:v", "libx264", "-preset", "medium", "-crf", "18"}},
		{"h264_nvenc", []string{"-c:v", "h264_nvenc", "-cq", "20"}},
		{"h264_qsv", []string{"-c:v", "h264_qsv", "-global_quality", "20"}},
	}
	for _, candidate := range candidates {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		args := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=black:s=320x240:d=0.2", "-threads", "1"}
		args = append(args, candidate.args...)
		args = append(args, "-pix_fmt", "yuv420p", "-f", "null", "-")
		err := exec.CommandContext(ctx, ffmpeg, args...).Run()
		cancel()
		if err == nil {
			return candidate.name, candidate.args
		}
	}
	return "", nil
}

type assemblyMedia struct {
	Width, Height int
	Audio         bool
}

func probeAssemblyMedia(ffmpeg, path string) (assemblyMedia, error) {
	probe := getenvDefault("ASSEMBLY_FFPROBE_PATH", os.Getenv("FFPROBE_PATH"))
	if probe == "" {
		if filepath.IsAbs(ffmpeg) {
			probe = filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
		} else {
			probe = "ffprobe"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "stream=codec_type,width,height", "-of", "json", path).Output()
	if e != nil {
		return assemblyMedia{}, fmt.Errorf("无法读取分镜媒体信息，请检查 FFPROBE_PATH")
	}
	var data struct {
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		}
	}
	if json.Unmarshal(b, &data) != nil {
		return assemblyMedia{}, fmt.Errorf("分镜媒体信息无效")
	}
	m := assemblyMedia{}
	for _, s := range data.Streams {
		if s.Type == "video" && m.Width == 0 {
			m.Width = s.Width
			m.Height = s.Height
		}
		if s.Type == "audio" {
			m.Audio = true
		}
	}
	if m.Width < 2 || m.Height < 2 {
		return m, fmt.Errorf("分镜没有有效视频轨")
	}
	return m, nil
}

// targetVideoDimensions returns the canonical frame for the selected output
// resolution. The upstream video model returns dimensions such as 1312x736;
// publishing those directly leaves the UI with non-standard sizes and makes
// portrait projects depend on provider-specific behavior.
func targetVideoDimensions(aspect string) (int, int) {
	w, h := 1280, 720
	if strings.EqualFold(getenvDefault("VIDEO_RESOLUTION", "720P"), "1080P") {
		w, h = 1920, 1080
	}
	if aspect == "9:16" {
		return h, w
	}
	return w, h
}

// assembleWithFFmpeg 逐段响度对齐后拼接
func (s *Service) assembleWithFFmpeg(paths []string, shots []model.Shot) (string, error) {
	ffmpeg := getenvDefault("ASSEMBLY_FFMPEG_PATH", getenvDefault("FFMPEG_PATH", "ffmpeg"))
	outDir := s.storageDir("exports")
	if len(paths) == 0 || len(paths) != len(shots) {
		return "", fmt.Errorf("没有可合成的分镜")
	}
	f, err := os.CreateTemp(outDir, "assembly-*.mp4")
	if err != nil {
		return "", err
	}
	outPath := f.Name()
	if err = f.Close(); err != nil {
		return "", err
	}
	success := false
	defer func() {
		if !success {
			os.Remove(outPath)
		}
	}()

	enc, encArgs := pickEncoder(ffmpeg)
	if enc == "" {
		return "", fmt.Errorf("服务器没有可用的 H.264 编码器，请配置支持 libx264 的 ASSEMBLY_FFMPEG_PATH；未生成替代格式")
	}
	log.Printf("[assemble] 使用编码器: %s", enc)

	media := make([]assemblyMedia, len(paths))
	for i, path := range paths {
		m, e := probeAssemblyMedia(ffmpeg, path)
		if e != nil {
			return "", e
		}
		media[i] = m
	}
	w, h := targetVideoDimensions("16:9")
	if s.db != nil && shots[0].ProjectID != "" {
		p, e := s.getProjectRaw(shots[0].ProjectID)
		if e != nil {
			return "", e
		}
		w, h = targetVideoDimensions(p.Aspect)
	}
	var filters, concatTags []string
	for i := range paths {
		dur := shots[i].Duration
		if dur <= 0 || dur > 180 {
			return "", fmt.Errorf("分镜时长无效")
		}
		filters = append(filters, fmt.Sprintf("[%d:v]trim=0:%.3f,setpts=PTS-STARTPTS,fps=24,scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,setsar=1,format=yuv420p[v%d]", i, dur, w, h, w, h, i))
		if media[i].Audio {
			filters = append(filters, fmt.Sprintf("[%d:a]atrim=0:%.3f,asetpts=PTS-STARTPTS,loudnorm=I=-20:TP=-3:LRA=11,aresample=48000,aformat=channel_layouts=stereo,apad,atrim=0:%.3f[a%d]", i, dur, dur, i))
		} else {
			filters = append(filters, fmt.Sprintf("anullsrc=r=48000:cl=stereo,atrim=0:%.3f,asetpts=PTS-STARTPTS[a%d]", dur, i))
		}
		concatTags = append(concatTags, fmt.Sprintf("[v%d][a%d]", i, i))
	}
	filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=1[v][a]",
		strings.Join(concatTags, ""), len(paths)))

	args := []string{"-y", "-v", "error", "-filter_complex_threads", "1"}
	for _, p := range paths {
		args = append(args, "-i", p)
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[v]")
	args = append(args, encArgs...)
	args = append(args, "-threads", "2")
	args = append(args,
		"-map", "[a]", // ★ 必须映射音频，否则成片无声
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
		"-movflags", "+faststart",
		outPath,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Env = append(os.Environ())
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("H.264 合成失败或超时，原成片已保留，请检查编码器与分镜")
	}
	if err = os.Chmod(outPath, 0644); err != nil {
		return "", fmt.Errorf("无法设置成片交付权限，未发布")
	}
	success = true
	return outPath, nil
}

// extractFrames 抽帧
func (s *Service) extractFrames(projectID, shotID, videoPath string) {
	ffmpeg := getenvDefault("FFMPEG_PATH", "ffmpeg")
	dir, err := os.MkdirTemp(s.storageDir("frames"), "frames-")
	if err != nil {
		return
	}
	kept := false
	defer func() {
		if !kept {
			os.RemoveAll(dir)
		}
	}()
	var paths []string
	for i := 1; i <= 3; i++ {
		if s.checkLease() != nil {
			return
		}
		out := fmt.Sprintf("%s/f%d.jpg", dir, i)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		e := exec.CommandContext(ctx, ffmpeg, "-y", "-v", "error", "-ss", fmt.Sprintf("%.1f", float64(i)*1.4), "-i", videoPath, "-frames:v", "1", "-vf", "scale=480:-1", "-q:v", "3", out).Run()
		cancel()
		if e != nil {
			continue
		}
		if _, e := os.Stat(out); e == nil {
			if os.Chmod(out, 0644) != nil {
				return
			}
			paths = append(paths, s.publicURL(out))
		}
	}
	if len(paths) == 0 {
		return
	}
	if os.Chmod(dir, 0755) != nil {
		return
	}
	publish := func(tx *sql.Tx) error {
		var current string
		if e := tx.QueryRow(`SELECT COALESCE(video_url,'') FROM shots WHERE id=$1 AND project_id=$2`, shotID, projectID).Scan(&current); e != nil {
			return e
		}
		if current != s.publicURL(videoPath) {
			return ErrLeaseLost
		}
		if _, e := tx.Exec(`DELETE FROM shot_frames WHERE shot_id=$1`, shotID); e != nil {
			return e
		}
		for _, path := range paths {
			if _, e := tx.Exec(`INSERT INTO shot_frames(shot_id,project_id,path) VALUES($1,$2,$3)`, shotID, projectID, path); e != nil {
				return e
			}
		}
		return nil
	}
	if s.job != nil {
		err = s.jobMutation(s.job, publish)
	} else {
		tx, e := s.db.Begin()
		if e != nil {
			return
		}
		defer tx.Rollback()
		if _, _, _, _, err = lockQueueProject(tx, projectID); err != nil {
			return
		}
		var id string
		if err = tx.QueryRow(`SELECT id FROM shots WHERE id=$1 AND project_id=$2 FOR UPDATE`, shotID, projectID).Scan(&id); err != nil {
			return
		}
		if err = s.fenceTx(tx, projectID, shotID, ""); err != nil {
			return
		}
		if err = publish(tx); err == nil {
			err = tx.Commit()
		}
	}
	kept = err == nil
}
