package service

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"drama-platform/internal/model"
)

// ══════════════════ 力萌 API ══════════════════

type LiMengClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewLiMeng() *LiMengClient {
	return &LiMengClient{
		BaseURL: getenvDefault("LIMENG_BASE_URL", "http://localhost:8081/v1"),
		APIKey:  os.Getenv("LIMENG_API_KEY"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *LiMengClient) headers() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + c.APIKey,
		"Content-Type":  "application/json",
	}
}

func (c *LiMengClient) req(method, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &UpstreamHTTPError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type CreateVideoResp struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

var errUpstreamTaskMismatch = errors.New("upstream task id mismatch")

type VideoTaskResp struct {
	TaskID         string  `json:"task_id"`
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	URL            string  `json:"url"`
	Error          string  `json:"error"`
	ChargeMicros   int64   `json:"charge_micros"`
	Resolution     string  `json:"resolution"`
	Duration       float64 `json:"duration"`
	Watermarked    bool    `json:"watermarked"`
	ElapsedSeconds int     `json:"elapsed_seconds"`
}

func (c *LiMengClient) CreateVideo(body map[string]interface{}) (*CreateVideoResp, error) {
	var r CreateVideoResp
	if err := c.req("POST", "/videos", body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *LiMengClient) GetVideo(id string) (*VideoTaskResp, error) {
	var r VideoTaskResp
	if id == "" {
		return nil, errors.New("missing upstream task id")
	}
	if err := c.req("GET", "/videos/"+url.PathEscape(id), nil, &r); err != nil {
		return nil, err
	}
	// The media id is authoritative. task_id is an auxiliary identifier and
	// may differ for the same task; only reject a non-empty task_id when no
	// media id was returned and the auxiliary id conflicts.
	if r.ID != "" && r.ID != id {
		return nil, errUpstreamTaskMismatch
	}
	if r.ID == "" && r.TaskID != "" && r.TaskID != id {
		return nil, errUpstreamTaskMismatch
	}
	r.ID = id
	return &r, nil
}

// GetImage accepts omitted identity for the existing protocol, but never an
// explicit conflicting id/task_id. The queried identity is authoritative.
func (c *LiMengClient) GetImage(id string) (*VideoTaskResp, error) {
	if id == "" {
		return nil, errors.New("missing upstream task id")
	}
	var result VideoTaskResp
	if err := c.req("GET", "/images/"+url.PathEscape(id), nil, &result); err != nil {
		return nil, err
	}
	if result.ID != "" && result.ID != id {
		return nil, errUpstreamTaskMismatch
	}
	if result.ID == "" && result.TaskID != "" && result.TaskID != id {
		return nil, errUpstreamTaskMismatch
	}
	result.ID = id
	return &result, nil
}

// ══════════════════ 成本估算 ══════════════════

// EstimateShotCost 估算单段成本（积分）
// 力萌：standard ¥0.2/s、720P ¥0.4/s、1080P ¥0.8/s，最低 ¥0.8
func EstimateShotCost(seconds float64, resolution string) int64 {
	perSec := map[string]int64{"standard": 20, "720P": 40, "1080P": 80} // 保持部署前线上现行费率
	p, ok := perSec[resolution]
	if !ok {
		p = 40
	}
	cost := int64(seconds) * p
	if cost < 80 {
		cost = 80
	}
	return cost
}

// ══════════════════ 定妆照 ══════════════════

func (s *Service) GenerateKeyVisual(projectID string) error {
	_, err := s.EnqueueJob(JobKeyVisual, projectID, "", 0, "")
	return err
}

// waitKeyVisual 轮询定妆照直到出图。可被启动恢复流程复用。
func (s *Service) waitKeyVisual(projectID, userID, taskID string) error {
	lm := NewLiMeng()
	var finalURL string
	var chargeMicros int64
	var lastPollErr string

	for i := 0; i < 240; i++ {
		if i > 0 {
			time.Sleep(5 * time.Second)
		}
		st, pollErr := lm.GetImage(taskID)
		if pollErr != nil {
			if errors.Is(pollErr, errUpstreamTaskMismatch) {
				return pollErr
			}
			err := pollErr
			lastPollErr = err.Error()
			if i%12 == 0 {
				log.Printf("[keyvisual] 第 %d 次轮询出错: %v", i+1, err)
			}
			continue
		}
		switch st.Status {
		case "succeeded":
			finalURL = st.URL
			chargeMicros = st.ChargeMicros
			log.Printf("[keyvisual] 成功，耗时约 %ds", (i+1)*5)
		case "failed", "cancelled":
			// 只清任务 ID；项目状态由 worker 的 FailJob 决定（重试期间不应判死）
			tx, e := s.db.Begin()
			if e != nil {
				return e
			}
			defer tx.Rollback()
			if _, _, _, _, e = lockQueueProject(tx, projectID); e != nil {
				return e
			}
			if e = s.fenceTx(tx, projectID, "", taskID); e != nil {
				return e
			}
			if e = settlementExecOne(tx, "legacy image failure", `UPDATE projects SET keyvisual_task_id=NULL, updated_at=now() WHERE id=$1 AND keyvisual_task_id=$2`, projectID, taskID); e != nil {
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			return fmt.Errorf("定妆照生成失败：%s", st.Error)
		}
		if finalURL != "" {
			break
		}
		if i%12 == 0 {
			log.Printf("[keyvisual] 轮询中 %ds status=%s", (i+1)*5, st.Status)
		}
	}
	if finalURL == "" {
		msg := "定妆照生成超时（20 分钟未出图，可能上游排队）"
		if lastPollErr != "" {
			msg = "定妆照生成超时（最后一次轮询错误：" + lastPollErr + "）"
		}
		// Unknown/pending tasks remain recoverable by the same upstream id.
		return errors.New(msg)
	}

	facts := GenerationSettlement{Kind: "image", TaskID: taskID, ProjectID: projectID, ChargeMicros: chargeMicros}
	if legacy, e := s.checkGenerationDelivery(facts); e != nil {
		return e
	} else if legacy {
		return nil
	}
	p, e := s.getProjectRaw(projectID)
	if e != nil {
		return e
	}
	localPath, err := s.stageGenerationArtifact(finalURL, "image", taskID, p.Aspect)
	if err != nil {
		return fmt.Errorf("定妆照交付失败：%w", err)
	}
	publicURL := s.publicURL(localPath)

	facts.URL = publicURL
	settled, err := s.SettleGeneration(facts)
	if err != nil {
		return err
	}
	if settled {
		s.Log(userID, projectID, "", "keyvisual_ready", "KEYVISUAL_GENERATING", "KEYVISUAL_REVIEW", "")
	}
	return nil
}

// RecoverStuckJobs 服务启动时调用：
//   - 有 keyvisual_task_id 的 → 继续轮询（不重复下单，不重复扣费）
//   - 没有任务 ID 但卡在生成中的 → 标记失败，让用户能重试
func (s *Service) RecoverStuckJobs() { s.adoptLegacyJobs("") }

// GenerateShots only reserves and enqueues. Completed shots are never re-created.
func (s *Service) GenerateShots(projectID, userID string, seqs []int) (int, error) {
	n, _, err := s.queueShots(projectID, userID, seqs, false, false, "")
	return n, err
}

// PollShots is compatible with old clients; only the leased worker applies results.
func (s *Service) PollShots(projectID string) { s.adoptLegacyJobs(projectID) }

func generationArtifactKey(taskID string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(taskID)))[:20]
}

func (s *Service) onShotSucceeded(projectID, shotID, shotCode string, task *VideoTaskResp) error {
	if task == nil || task.ID == "" || task.Status != "succeeded" {
		return errors.New("invalid completed task")
	}
	facts := GenerationSettlement{Kind: "video", TaskID: task.ID, ProjectID: projectID, ShotID: shotID, ChargeMicros: task.ChargeMicros, Duration: task.Duration, Width: widthOf(task.Resolution, s), Height: heightOf(task.Resolution, s)}
	if legacy, e := s.checkGenerationDelivery(facts); e != nil {
		return e
	} else if legacy {
		return nil
	}
	p, e := s.getProjectRaw(projectID)
	if e != nil {
		return e
	}
	localPath, err := s.stageGenerationArtifact(task.URL, "video", task.ID, p.Aspect)
	if err != nil {
		// Keep QUEUED and the same upstream id; retry delivery, not paid generation.
		if s.job != nil {
			if e := s.jobMutation(s.job, func(tx *sql.Tx) error {
				return settlementExecOne(tx, "download diagnostic", `UPDATE shots SET error_msg='视频下载失败，等待原任务恢复',updated_at=now() WHERE id=$1 AND project_id=$2 AND upstream_id=$3 AND status='QUEUED'`, shotID, projectID, task.ID)
			}); e != nil {
				return e
			}
		}
		return fmt.Errorf("download generated video: %w", err)
	}
	facts.URL = s.publicURL(localPath)
	settled, err := s.SettleGeneration(facts)
	if err != nil {
		return err
	}
	if settled {
		// The raw H3 response reports 1312x736 even when the published,
		// normalized file is 720x1280. Keep the settlement receipt unchanged but
		// make the shot metadata match the actual deliverable.
		if m, probeErr := probeAssemblyMedia(getenvDefault("ASSEMBLY_FFMPEG_PATH", getenvDefault("FFMPEG_PATH", "ffmpeg")), localPath); probeErr == nil {
			_, _ = s.db.Exec(`UPDATE shots SET width=$1,height=$2 WHERE id=$3 AND project_id=$4`, m.Width, m.Height, shotID, projectID)
		}
		s.extractFrames(projectID, shotID, localPath)
	}
	return nil
}

// ══════════════════ 合成 ══════════════════

func (s *Service) AssembleFinal(projectID string) error {
	if s.job == nil {
		_, err := s.EnqueueJob(JobAssemble, projectID, "", 0, "")
		return err
	}
	if err := s.checkLease(); err != nil {
		return err
	}
	shots, err := s.listShots(projectID)
	if err != nil {
		return err
	}
	if len(shots) == 0 {
		return errors.New("没有可合成的分镜")
	}
	for _, sh := range shots {
		if sh.Status != "APPROVED" || sh.VideoURL == "" {
			return errors.New("全部分镜有视频并通过审片后才能合成")
		}
	}
	if err = s.jobMutation(s.job, func(tx *sql.Tx) error {
		if err := ensureGenerationPhase(tx, projectID, JobAssemble); err != nil {
			return err
		}
		return settlementExecOne(tx, "assembly start", `UPDATE projects SET status='ASSEMBLING',updated_at=now() WHERE id=$1`, projectID)
	}); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "assemble-"+generationArtifactKey(s.job.ID)+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	var localPaths []string
	for _, sh := range shots {
		if err = s.checkLease(); err != nil {
			return err
		}
		lp := filepath.Join(tmpDir, fmt.Sprintf("%03d.mp4", sh.Seq))
		if err = downloadFile(sh.VideoURL, lp); err != nil {
			return fmt.Errorf("下载分镜失败：%w", err)
		}
		localPaths = append(localPaths, lp)
	}
	if err = s.checkLease(); err != nil {
		return err
	}
	outPath, err := s.assembleWithFFmpeg(localPaths, shots)
	if err != nil {
		return err
	}
	published := false
	notifyCurrent := false
	defer func() {
		if !published {
			os.Remove(outPath)
		}
	}()
	publicURL := s.publicURL(outPath)
	err = s.jobMutation(s.job, func(tx *sql.Tx) error {
		if err := ensureGenerationPhase(tx, projectID, JobAssemble); err != nil {
			return err
		}
		for _, sh := range shots {
			var url, status string
			if e := tx.QueryRow(`SELECT COALESCE(video_url,''),status FROM shots WHERE id=$1 AND project_id=$2`, sh.ID, projectID).Scan(&url, &status); e != nil {
				return e
			}
			if status != "APPROVED" || url != sh.VideoURL {
				return errors.New("合成期间分镜已变更，请重新合成")
			}
		}
		current, e := s.recordRunArtifactTx(tx, "final", publicURL, "", shots)
		if e != nil {
			return e
		}
		if current {
			notifyCurrent = true
			if e := settlementExecOne(tx, "assembly publish", `UPDATE projects
			SET status='COMPLETED',final_url=$1,final_run_id=NULLIF($3,''),error_msg=NULL,updated_at=now()
			WHERE id=$2`, publicURL, projectID, s.job.RunID); e != nil {
				return e
			}
		}
		// Completion and publication commit together; this lease can no longer be reclaimed
		// before its success notification. Only the transaction winner may notify.
		return settlementExecOne(tx, "assembly complete", `UPDATE jobs SET status='done',phase='done',updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, s.job.ID, s.job.LeaseToken)
	})
	if err != nil {
		return err
	}
	published = true
	var email, title string
	if notifyCurrent && s.db.QueryRow(`SELECT u.email,p.title FROM users u JOIN projects p ON p.user_id=u.id WHERE p.id=$1`, projectID).Scan(&email, &title) == nil && email != "" {
		s.SendFinalReady(email, title, publicURL)
	}
	return nil
}

// ══════════════════ 审片操作 ══════════════════

func (s *Service) ApproveShot(projectID string, seq int) error {
	res, err := s.db.Exec(`UPDATE shots SET status='APPROVED', updated_at=now() WHERE project_id=$1 AND seq=$2 AND status IN ('GENERATED','APPROVED') AND COALESCE(video_url,'')<>''`, projectID, seq)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("分镜不存在")
	}
	s.Log("", projectID, "", "approve_shot", "", "", fmt.Sprintf("seq=%d", seq))
	return nil
}

func (s *Service) UnapproveShot(projectID string, seq int) error {
	res, err := s.db.Exec(`UPDATE shots SET status='GENERATED', updated_at=now() WHERE project_id=$1 AND seq=$2 AND status IN ('APPROVED','GENERATED') AND COALESCE(video_url,'')<>''`, projectID, seq)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("分镜尚未完成，不能变更审片状态")
	}
	return nil
}

// RegenerateShot is the interim manual submission boundary. Task3 should replace
// the network call with a queued attempt while keeping user -> project -> shot ->
// receipt locks and installing a new identity only after preflight succeeds.
func (s *Service) RegenerateShot(projectID string, seq int, override bool) error {
	_, _, err := s.queueShots(projectID, "", []int{seq}, true, override, "")
	return err
}

// ══════════════════ 辅助 ══════════════════

func (s *Service) storageDir(sub string) string {
	dir := filepath.Join(getenvDefault("STORAGE_DIR", "/opt/ai-drama-studio/storage"), sub)
	os.MkdirAll(dir, 0o755)
	return dir
}

func (s *Service) publicURL(localPath string) string {
	base := getenvDefault("PUBLIC_BASE_URL", "http://localhost:8088")
	root, err := filepath.Abs(getenvDefault("STORAGE_DIR", "/opt/ai-drama-studio/storage"))
	if err != nil {
		return ""
	}
	path, err := filepath.Abs(localPath)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return strings.TrimRight(base, "/") + "/files/" + filepath.ToSlash(rel)
}

func charsForShot(s *Service, sh model.Shot) []model.Character {
	chars, _ := s.listCharacters(sh.ProjectID)
	out := []model.Character{}
	for _, c := range chars {
		if c.Code == sh.SpeakerCode {
			out = append(out, c)
		}
	}
	return out
}

func scenesForShot(s *Service, sh model.Shot) []model.Scene {
	if sh.SceneID == "" {
		return nil
	}
	var sc model.Scene
	var propsRaw []byte
	err := s.db.QueryRow(`SELECT id,project_id,code,name,location,time_of_day,lighting,color_temp,props
	                      FROM scenes WHERE id=$1`, sh.SceneID).
		Scan(&sc.ID, &sc.ProjectID, &sc.Code, &sc.Name, &sc.Location, &sc.TimeOfDay, &sc.Lighting, &sc.ColorTemp, &propsRaw)
	if err != nil {
		return nil
	}
	sc.Props = decodeStringArray(propsRaw)
	return []model.Scene{sc}
}

func widthOf(res string, s *Service) int {
	return map[string]int{"standard": 832, "720P": 1312, "1080P": 1920}[res]
}
func heightOf(res string, s *Service) int {
	return map[string]int{"standard": 480, "720P": 736, "1080P": 1088}[res]
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".media-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if n, copyErr := io.Copy(f, resp.Body); copyErr != nil {
		return copyErr
	} else if n == 0 {
		return errors.New("empty media response")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Chmod(0644); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), dest)
}

func (c *LiMengClient) downloadTo(url, dir, filename string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, filename)
	if err := downloadFile(url, out); err != nil {
		return "", err
	}
	return out, nil
}
