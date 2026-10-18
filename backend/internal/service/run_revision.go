package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"drama-platform/internal/model"
)

type revisionScope struct {
	Instruction  string `json:"instruction"`
	TotalSeconds int    `json:"totalSeconds,omitempty"`
	Aspect       string `json:"aspect,omitempty"`
}
type revisionChange struct {
	Seq          int     `json:"seq"`
	VisualPrompt string  `json:"visualPrompt"`
	Dialogue     *string `json:"dialogue,omitempty"`
	CameraMove   *string `json:"cameraMove,omitempty"`
}
type revisionPlan struct {
	Snapshot string
	Changes  []revisionChange
	Estimate int64
}
type revisionQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func revisionSnapshot(q revisionQuerier, project string) (string, error) {
	var snapshot string
	err := q.QueryRow(`SELECT jsonb_build_object('project',jsonb_build_object('id',p.id,'user_id',p.user_id,'title',p.title,'idea',p.idea,
 'aspect',p.aspect,'total_seconds',p.total_seconds,'status',p.status,'style_config',p.style_config,
 'keyvisual_url',p.keyvisual_url,'final_url',p.final_url,'final_run_id',p.final_run_id),
 'shots',COALESCE((SELECT jsonb_agg(to_jsonb(s)-'updated_at' ORDER BY s.seq) FROM shots s WHERE s.project_id=p.id),'[]'::jsonb),
 'characters',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM characters c WHERE c.project_id=p.id),'[]'::jsonb),
 'scenes',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM scenes c WHERE c.project_id=p.id),'[]'::jsonb),
 'frames',COALESCE((SELECT jsonb_agg(to_jsonb(f) ORDER BY f.id) FROM shot_frames f JOIN shots s ON s.id=f.shot_id WHERE s.project_id=p.id),'[]'::jsonb))::text
 FROM projects p WHERE p.id=$1`, project).Scan(&snapshot)
	return snapshot, err
}

// Plan before creating a rechargeable run. The first revision contract keeps
// cast, locations, duration and aspect fixed and edits only existing shot content.
func (s *Service) planRevision(project string, raw json.RawMessage, resolution string) (*revisionPlan, error) {
	var scope revisionScope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&scope); err != nil {
		return nil, errors.New("修改范围无效")
	}
	scope.Instruction = strings.TrimSpace(scope.Instruction)
	if scope.Instruction == "" || len([]rune(scope.Instruction)) > 2000 {
		return nil, errors.New("请填写 1–2000 字的修改要求")
	}
	p, err := s.getProjectRaw(project)
	if err != nil {
		return nil, err
	}
	if err = s.db.QueryRow(`SELECT COALESCE(final_url,'') FROM projects WHERE id=$1`, project).Scan(&p.FinalURL); err != nil {
		return nil, err
	}
	if p.Status != "COMPLETED" || p.FinalURL == "" || p.KeyvisualURL == "" {
		return nil, errors.New("请先完成当前成片，再提交修改")
	}
	if scope.TotalSeconds != 0 && scope.TotalSeconds != p.TotalSeconds || scope.Aspect != "" && scope.Aspect != p.Aspect {
		return nil, errors.New("当前修改保留原时长和画幅；请在现有分镜内描述修改")
	}
	var active bool
	if err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND status IN ('queued','running','reconcile'))`, project).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, errors.New("原任务仍在核对或结算，完成后再修改")
	}
	shots, err := s.listShots(project)
	if err != nil {
		return nil, err
	}
	if len(shots) == 0 {
		return nil, errors.New("缺少可修改的分镜")
	}
	for _, sh := range shots {
		if sh.VideoURL == "" || (sh.Status != "APPROVED" && sh.Status != "GENERATED") {
			return nil, errors.New("当前分镜尚未全部交付")
		}
	}
	snapshot, err := revisionSnapshot(s.db, project)
	if err != nil {
		return nil, err
	}
	contextShots, _ := json.Marshal(shots)
	instruction := `你是分镜编辑器。保留现有角色、场景、时长和画幅，只修改用户指定部分，其他镜头不返回。输出 JSON {"changes":[{"seq":原seq,"visualPrompt":"完整新画面描述","dialogue":"可选新中文台词不超过20字","cameraMove":"可选单一运镜"}]}。不能实现的要求返回 {"changes":[],"unsupported":"说明"}。禁止编造 seq，禁止输出网址、价格、工具调用。`
	body, _ := json.Marshal(map[string]any{"model": getenvDefault("LIMENG_TEXT_MODEL", "qwen3.8-27b-local"), "messages": []map[string]string{{"role": "system", "content": instruction}, {"role": "user", "content": "现有分镜：" + string(contextShots) + "\n修改要求：" + scope.Instruction}}, "max_tokens": 8000, "response_format": map[string]string{"type": "json_object"}, "chat_template_kwargs": map[string]bool{"enable_thinking": false}})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", getenvDefault("LIMENG_BASE_URL", "http://localhost:8081/v1")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("模型地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("LIMENG_API_KEY"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.New("修改方案生成失败，请稍后重试；尚未创建收费任务")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("修改方案生成失败（HTTP %d），尚未创建收费任务", resp.StatusCode)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&envelope); err != nil || len(envelope.Choices) != 1 {
		return nil, errors.New("修改方案响应无效")
	}
	var output struct {
		Changes     []revisionChange `json:"changes"`
		Unsupported string           `json:"unsupported"`
	}
	d := json.NewDecoder(strings.NewReader(envelope.Choices[0].Message.Content))
	d.DisallowUnknownFields()
	if err = d.Decode(&output); err != nil || output.Unsupported != "" || len(output.Changes) == 0 || len(output.Changes) > len(shots) {
		return nil, errors.New("当前要求无法形成有效修改范围，请在现有分镜内描述具体改动")
	}
	bySeq := map[int]model.Shot{}
	for _, sh := range shots {
		bySeq[sh.Seq] = sh
	}
	seen := map[int]bool{}
	plan := &revisionPlan{Snapshot: snapshot}
	for _, c := range output.Changes {
		sh, ok := bySeq[c.Seq]
		if !ok || seen[c.Seq] {
			return nil, errors.New("修改方案包含无效或重复分镜")
		}
		seen[c.Seq] = true
		c.VisualPrompt = strings.TrimSpace(c.VisualPrompt)
		if c.VisualPrompt == "" || len([]rune(c.VisualPrompt)) > 4000 {
			return nil, errors.New("修改画面描述无效")
		}
		if c.Dialogue != nil && len([]rune(*c.Dialogue)) > DialogueMax {
			return nil, errors.New("修改台词超过字数限制")
		}
		if c.CameraMove != nil {
			if strings.TrimSpace(*c.CameraMove) == "" || len([]rune(*c.CameraMove)) > 64 {
				return nil, errors.New("修改运镜无效")
			}
			for _, w := range forbiddenMoveWords {
				if strings.Contains(*c.CameraMove, w) {
					return nil, errors.New("修改运镜必须是单一动作")
				}
			}
		}
		if c.VisualPrompt == sh.VisualPrompt && (c.Dialogue == nil || *c.Dialogue == sh.Dialogue) && (c.CameraMove == nil || *c.CameraMove == sh.CameraMove) {
			continue
		}
		plan.Changes = append(plan.Changes, c)
		plan.Estimate += EstimateShotCost(sh.Duration, resolution)
	}
	if len(plan.Changes) == 0 {
		return nil, errors.New("修改方案没有内容变化，未创建收费任务")
	}
	return plan, nil
}

func (s *Service) saveRevisionTx(tx *sql.Tx, runID, project string, plan *revisionPlan) error {
	current, err := revisionSnapshot(tx, project)
	if err != nil {
		return err
	}
	if current != plan.Snapshot {
		return errors.New("制作内容已变化，请重新提交修改；未创建收费任务")
	}
	var active bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND status IN ('queued','running','reconcile'))`, project).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrRunNotAuthorized
	}
	raw, err := json.Marshal(plan.Changes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO generation_revision_plans(run_id,base_snapshot,changes) VALUES($1,$2::jsonb,$3::jsonb)`, runID, plan.Snapshot, string(raw))
	return err
}

// Applying the validated plan and checkpoint is atomic; a worker crash cannot
// clear a second set of attempts or charge unchanged footage again.
func (s *Service) applyRevision(ctx context.Context, runID, project string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	user, err := authorizeRunTx(tx, project, runID)
	if err != nil {
		return err
	}
	owner, _, _, _, err := lockQueueProject(tx, project)
	if err != nil {
		return err
	}
	if owner != user {
		return ErrRunNotAuthorized
	}
	var snapshot, raw string
	var applied sql.NullTime
	if err = tx.QueryRow(`SELECT base_snapshot::text,changes::text,applied_at FROM generation_revision_plans WHERE run_id=$1 FOR UPDATE`, runID).Scan(&snapshot, &raw, &applied); err != nil {
		return err
	}
	if applied.Valid {
		return tx.Commit()
	}
	current, err := revisionSnapshot(tx, project)
	if err != nil {
		return err
	}
	if current != snapshot {
		return errors.New("修改基线已变化")
	}
	var changes []revisionChange
	if err = json.Unmarshal([]byte(raw), &changes); err != nil {
		return err
	}
	for _, c := range changes {
		var id, task, status, url, delivered string
		if err = tx.QueryRow(`SELECT id,COALESCE(upstream_id,''),status,COALESCE(video_url,''),COALESCE(delivered_upstream_id,'') FROM shots WHERE project_id=$1 AND seq=$2 FOR UPDATE`, project, c.Seq).Scan(&id, &task, &status, &url, &delivered); err != nil {
			return err
		}
		if err = protectLegacyShot(tx, user, project, id, task, status, url, delivered); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE shots SET visual_prompt=$1,dialogue=COALESCE($2,dialogue),camera_move=COALESCE($3,camera_move),
 status='PENDING',retry_count=0,upstream_id=NULL,delivered_upstream_id=NULL,video_url=NULL,duration_actual=0,width=0,height=0,charge_micros=0,error_msg=NULL,updated_at=now()
 WHERE id=$4`, c.VisualPrompt, c.Dialogue, c.CameraMove, id); err != nil {
			return err
		}
		// Frames are retained in base_snapshot and their media files remain on disk.
		if _, err = tx.Exec(`DELETE FROM shot_frames WHERE shot_id=$1`, id); err != nil {
			return err
		}
	}
	// Bind the unchanged completed assets to their known source run. Legacy
	// assets lacking evidence stay in the snapshot without invented attribution.
	if _, err = tx.Exec(`INSERT INTO generation_artifact_reuses(run_id,artifact_id)
 SELECT $1,source.id FROM (
 SELECT DISTINCT ON(a.kind,a.target_id) a.id,a.kind,a.target_id
 FROM generation_artifacts a JOIN jobs j ON j.id=a.job_id JOIN generation_runs old ON old.id=a.run_id
 JOIN generation_runs current ON current.id=$1 JOIN projects p ON p.id=current.project_id
 LEFT JOIN shots sh ON sh.id=a.target_id AND sh.project_id=p.id
 WHERE old.project_id=p.id AND old.user_id=current.user_id AND old.project_version<current.project_version AND j.status='done'
 AND ((a.kind='image' AND a.result_url=p.keyvisual_url) OR (a.kind='video' AND sh.status IN ('GENERATED','APPROVED') AND a.result_url=sh.video_url AND a.upstream_id=sh.delivered_upstream_id))
 ORDER BY a.kind,a.target_id,old.project_version DESC,a.id DESC
 ) source ON CONFLICT DO NOTHING`, runID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE projects SET status='SHOTS_REVIEW',error_msg=NULL,updated_at=now() WHERE id=$1`, project); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE generation_revision_plans SET applied_at=now() WHERE run_id=$1`, runID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE generation_runs SET checkpoint='revision_applied',updated_at=now() WHERE id=$1`, runID); err != nil {
		return err
	}
	return tx.Commit()
}
