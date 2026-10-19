package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"drama-platform/internal/model"
)

// ══════════════════ 常量 ══════════════════

const (
	ShotCount     = 9 // 默认段数，超长按比例扩展
	DialogueMax   = 20
	ShotMaxRetry  = 2
	CreditPerYuan = 100 // 1元 = 100积分
	PollInterval  = 8 * time.Second
	ShotTimeout   = 75 * time.Minute
)

// ══════════════════ 剧本生成 ══════════════════

var forbiddenMoveWords = []string{
	"同时", "并且", "叠加", "加上", "配合", "一边", "一面",
}

func (s *Service) GenerateScript(projectID string, opts map[string]string) (*model.ScriptOutput, []string, error) {
	token, err := s.claimScript(projectID)
	if err != nil {
		return nil, nil, err
	}
	defer s.releaseScript(projectID, token)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p, err := s.getProjectRaw(projectID)
	if err != nil {
		return nil, nil, err
	}

	// 根据总时长算段数：每段 5 秒
	shotCount := p.TotalSeconds / 5

	if opts == nil {
		opts = map[string]string{}
	}
	var stored []byte
	if err = s.db.QueryRow(`SELECT style_config FROM projects WHERE id=$1`, projectID).Scan(&stored); err != nil {
		return nil, nil, err
	}
	var style map[string]string
	json.Unmarshal(stored, &style)
	if opts["filmReference"] == "" {
		opts["filmReference"] = style["film_reference"]
	}
	if opts["mood"] == "" {
		opts["mood"] = style["mood"]
	}
	prompt := buildScriptPrompt(p, shotCount, opts)
	base := getenvDefault("LIMENG_BASE_URL", "https://comemoe.com/v1")
	// The provider currently serves qwen3.8-27b-local. Its thinking/JSON
	// behavior changes with provider deployments: the explicit
	// enable_thinking=false flag now truncates the final JSON, while the default
	// request returns complete JSON. Keep the model configurable and do not send
	// a thinking override unless the provider contract changes again.
	model := getenvDefault("LIMENG_TEXT_MODEL", "qwen3.8-27b-local")
	messages := []map[string]string{
		{"role": "system", "content": "你是结构化 JSON 生成器。禁止输出任何思考过程、分析、解释或 markdown 代码块标记，必须直接以 { 开头输出最终 JSON。"},
		{"role": "system", "content": prompt},
	}

	var lastErr string

	for attempt := 1; attempt <= 3; attempt++ {
		log.Printf("[script] attempt=%d model=%s prompt_chars=%d thinking=provider-default", attempt, model, len(prompt))
		started := time.Now()
		body, _ := json.Marshal(map[string]interface{}{
			"model":           model,
			"messages":        messages,
			"max_tokens":      16000,
			"temperature":     0.7,
			"response_format": map[string]string{"type": "json_object"},
		})
		req, err := http.NewRequestWithContext(ctx, "POST", base+"/chat/completions",
			io.NopCloser(strings.NewReader(string(body))))
		if err != nil {
			return nil, nil, errors.New("模型请求地址配置无效")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+os.Getenv("LIMENG_API_KEY"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = "模型连接失败或超时，请稍后重试"
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = "模型响应读取失败"
			continue
		}

		log.Printf("[script] provider attempt=%d status=%d bytes=%d elapsed=%s", attempt, resp.StatusCode, len(raw), time.Since(started).Round(time.Millisecond))
		if resp.StatusCode != 200 {
			lastErr = fmt.Sprintf("模型服务暂不可用（HTTP %d）", resp.StatusCode)
			continue
		}

		var chatResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(raw, &chatResp) != nil || len(chatResp.Choices) == 0 {
			lastErr = "模型返回结构异常"
			continue
		}
		content := chatResp.Choices[0].Message.Content
		log.Printf("[script] response attempt=%d finish=%s content_chars=%d", attempt, chatResp.Choices[0].FinishReason, len(content))

		script, err := parseScriptJSON(content)
		if err != nil {
			log.Printf("[script] parse failure attempt=%d error=%v", attempt, err)
			lastErr = "模型输出不是合法 JSON"
			messages = append(messages, map[string]string{
				"role": "assistant", "content": truncate(content, 1000)})
			messages = append(messages, map[string]string{
				"role": "user", "content": "上次输出不是合法 JSON。只返回 JSON，不要 markdown 包裹，不要任何思考过程。"})
			continue
		}

		errs := validateScript(script, shotCount)
		if len(errs) == 0 {
			if err := s.saveScript(projectID, token, script); err != nil {
				return nil, nil, err
			}
			return script, nil, nil
		}

		lastErr = "校验未通过: " + strings.Join(errs, " / ")
		log.Printf("[script] validation attempt=%d errors=%d", attempt, len(errs))
		messages = append(messages, map[string]string{
			"role": "assistant", "content": truncate(content, 1200)})
		messages = append(messages, map[string]string{
			"role": "user",
			"content": fmt.Sprintf("有 %d 处问题必须全部修正后重新输出完整 JSON：\n%s\n\n只返回 JSON。",
				len(errs), strings.Join(errs, "\n"))})
	}

	log.Printf("[script] failed: %s", lastErr)
	return nil, nil, fmt.Errorf("剧本生成失败（%s）", lastErr)
}

func (s *Service) saveScript(projectID string, token int64, script *model.ScriptOutput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, _, _, _, err = lockQueueProject(tx, projectID); err != nil {
		return err
	}
	var live bool
	if err = tx.QueryRow(`SELECT status='SCRIPT_GENERATING' AND script_generation_token=$2 AND script_lease_until>now() FROM projects WHERE id=$1`, projectID, token).Scan(&live); err != nil {
		return err
	}
	if !live {
		return ErrLeaseLost
	}
	if err = scriptEditable(tx, projectID); err != nil {
		return err
	}
	styleJSON, _ := json.Marshal(script.StyleAnchor)
	if _, err := tx.Exec(`UPDATE projects SET title=$1, style_config=$2::jsonb,status='SCRIPT_REVIEW',script_lease_until=NULL, updated_at=now() WHERE id=$3`,
		script.Title, string(styleJSON), projectID); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM shots WHERE project_id=$1`, `DELETE FROM scenes WHERE project_id=$1`, `DELETE FROM characters WHERE project_id=$1`} {
		if _, err = tx.Exec(q, projectID); err != nil {
			return err
		}
	}

	for i, c := range script.Characters {
		_, err = tx.Exec(`INSERT INTO characters (project_id,code,name,age_range,appearance,wardrobe,persona,voice_profile,sort_order)
		         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			projectID, c.Code, c.Name, c.AgeRange, c.Appearance, c.Wardrobe, c.Persona, c.VoiceProfile, i)
		if err != nil {
			return err
		}
	}

	sceneID := map[string]string{}
	for _, sc := range script.Scenes {
		// JSONB 列显式转型，避免 pgx 对 []string/[]byte 的编码歧义
		propsJSON, _ := json.Marshal(sc.Props)
		var id string
		err = tx.QueryRow(`INSERT INTO scenes (project_id,code,name,location,time_of_day,lighting,color_temp,props)
		             VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb) RETURNING id`,
			projectID, sc.Code, sc.Name, sc.Location, sc.TimeOfDay, sc.Lighting, sc.ColorTemp, string(propsJSON)).Scan(&id)
		if err != nil {
			return err
		}
		sceneID[sc.Code] = id
	}

	var firstScene string
	for id := range sceneID {
		firstScene = id
		break
	}

	for i, sh := range script.Shots {
		_, err = tx.Exec(`INSERT INTO shots
		  (project_id,scene_id,shot_code,seq,duration,shot_size,camera_height,camera_move,
		   visual_prompt,speaker_code,dialogue,soundscape,music_cue,status)
		  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'PENDING')`,
			projectID, sceneID[sh.SceneCode], sh.ShotCode, i, 5.0,
			sh.ShotSize, sh.CameraHeight, sh.CameraMove, sh.VisualPrompt,
			sh.SpeakerCode, sh.Dialogue, sh.Soundscape, sh.MusicCue)
		if err != nil {
			return err
		}
		if firstScene == "" {
			firstScene = sceneID[sh.SceneCode]
		}
	}
	return tx.Commit()
}

// ══════════════════ 剧本校验 ══════════════════

func validateScript(s *model.ScriptOutput, wantShots int) []string {
	var errs []string

	if strings.TrimSpace(s.Title) == "" {
		errs = append(errs, "title 缺失")
	}
	chars := s.Characters
	if len(chars) < 2 {
		errs = append(errs, fmt.Sprintf("characters 至少 2 个，当前 %d", len(chars)))
	}
	codes := map[string]bool{}
	for i, c := range chars {
		if c.Code == "" {
			errs = append(errs, fmt.Sprintf("characters[%d] 缺 code", i))
		} else if codes[c.Code] {
			errs = append(errs, "code 重复："+c.Code)
		} else {
			codes[c.Code] = true
		}
		for _, f := range []string{"appearance", "wardrobe", "persona", "voice_profile"} {
			v := map[string]string{"appearance": c.Appearance, "wardrobe": c.Wardrobe,
				"persona": c.Persona, "voice_profile": c.VoiceProfile}[f]
			if strings.TrimSpace(v) == "" {
				errs = append(errs, fmt.Sprintf("characters[%d] 缺 %s", i, f))
			}
		}
	}

	sceneCodes := map[string]bool{}
	for _, sc := range s.Scenes {
		sceneCodes[sc.Code] = true
	}
	if len(s.Scenes) == 0 {
		errs = append(errs, "scenes 为空")
	}

	shots := s.Shots
	if len(shots) != wantShots {
		errs = append(errs, fmt.Sprintf("shots 必须正好 %d 段，当前 %d", wantShots, len(shots)))
	}
	dialogueCount := 0
	for i, sh := range shots {
		tag := fmt.Sprintf("Shot %d", i+1)
		if sh.SceneCode != "" && !sceneCodes[sh.SceneCode] {
			errs = append(errs, tag+" 引用了不存在的 scene_code")
		}
		move := strings.TrimSpace(sh.CameraMove)
		if move == "" {
			errs = append(errs, tag+" 缺 camera_move")
		} else {
			for _, w := range forbiddenMoveWords {
				if strings.Contains(move, w) {
					errs = append(errs, tag+" 运镜含叠加词「"+w+"」")
					break
				}
			}
		}
		if d := strings.TrimSpace(sh.Dialogue); d != "" {
			dialogueCount++
			if len([]rune(d)) > DialogueMax {
				errs = append(errs, fmt.Sprintf("%s 台词 %d 字超过 %d 字上限", tag, len([]rune(d)), DialogueMax))
			}
			if regexp.MustCompile(`<d>|\[Chinese\]|\[.*?\]|（|）\(|\(.*?\)|[A-Za-z]{3,}`).MatchString(d) {
				errs = append(errs, tag+" 台词混入标签或括号")
			}
			if sh.SpeakerCode != "" && !codes[sh.SpeakerCode] {
				errs = append(errs, tag+" speaker_code 不存在")
			}
		}
		if len(strings.TrimSpace(sh.VisualPrompt)) < 20 {
			errs = append(errs, tag+" visual_prompt 过短（需技术化描述）")
		}
	}
	if dialogueCount == 0 {
		errs = append(errs, "全片没有任何台词")
	}
	return errs
}

// ══════════════════ JSON 解析 ══════════════════

func parseScriptJSON(text string) (*model.ScriptOutput, error) {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "```"); i >= 0 {
		if j := strings.Index(t[i:], "\n"); j > 0 {
			t = t[i+j+1:]
		}
		if j := strings.LastIndex(t, "```"); j > 0 {
			t = t[:j]
		}
	}
	var out model.ScriptOutput
	if err := json.Unmarshal([]byte(t), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ══════════════════ 提示词构建 ══════════════════

func buildScriptPrompt(p *model.Project, shotCount int, opts map[string]string) string {
	aspectDesc := "横版 16:9 宽银幕构图"
	if p.Aspect == "9:16" {
		aspectDesc = "竖版 9:16 构图，人物居中或上下分置，适合手机全屏观看"
	}
	filmRef := opts["filmReference"]
	if filmRef == "" {
		filmRef = "电影感低调布光"
	}
	mood := opts["mood"]
	if mood == "" {
		mood = "克制、有张力"
	}

	// 极短脚本（1-3 段）需要不同的创作引导：塞不进完整叙事
	// 每个分支直接格式化，避免「无占位符却传参」产生的 %!(EXTRA …) 污染提示词
	var scaleGuide string
	switch {
	case shotCount == 1:
		scaleGuide = `## 篇幅提示（极短）

只有 1 个镜头、5 秒。这不是「短剧」，是**单镜头情绪片段**。
- 不要设计完整故事线，只抓一个瞬间：一个眼神、一个动作、一句台词
- 台词最多 1 句，15 字以内
- 画面要有一个明确的光影焦点`
	case shotCount <= 3:
		scaleGuide = fmt.Sprintf(`## 篇幅提示（很短）

只有 %d 个镜头、%d 秒。这是**一个片段**，不是完整故事。
- 只讲一件事的一个片段：一次对视、一次交谈的开头
- 不要铺陈起因经过结果，直接切入最有张力的那一刻
- 全片台词合计不超过 3 句`, shotCount, p.TotalSeconds)
	case shotCount <= 6:
		scaleGuide = fmt.Sprintf(`## 篇幅提示（短）

%d 个镜头、%d 秒。可以讲**一个完整的微型场景**。
- 有一个小转折即可，不要多层反转
- 前段建立情境，后段给出反转或情绪落点`, shotCount, p.TotalSeconds)
	default:
		scaleGuide = fmt.Sprintf(`## 篇幅提示

%d 个镜头、%d 秒。可以容纳**较完整的情节推进**。
- 建议在第 %d 段左右安排情绪转折点
- 注意节奏：不要全程同一景别，远近交替`, shotCount, p.TotalSeconds, shotCount/2)
	}

	return fmt.Sprintf(`你是一位资深电影编剧兼分镜师。把用户的一句话想法写成一条固定 %d 段、每段 5 秒、总长 %d 秒的短片脚本。

%s

## 铁律（违反任意一条即为不合格）

1. 输出必须是严格 JSON，不要 markdown 代码块，不要任何解释文字
2. 必须正好 %d 个分镜，shot_code 从 "Shot 1" 到 "Shot %d"
3. 每段时长固定 5.0 秒，不要改
4. 台词单段不超过 %d 个字。台词长了口型对不上，必须拆成多段说
5. 一个分镜只能有一种运镜。禁止"推进又拉远""一边推一边摇"这种叠加
6. 场景数量：v1 限制 1 个场景，所有分镜都用同一个 scene_code
7. 道具全片复用：props 是全片锚定物，写进每个分镜的 visual_prompt 描述里保持一致
8. visual_prompt 要写具体的技术描述，不要写风格形容词
   反例（不合格）："电影感画面""唯美""很有氛围"
   正例（合格）："低机位仰拍，暖黄台灯为唯一主光源，大面积暗部留白，只点亮眼睛和手"
9. 声音分三层：soundscape（环境底噪+动作音）、music_cue（非叙事音乐），都要具体到事件
10. 画幅：%s

## 台词标签的写法

dialogue 字段里只写台词原文，例如 "这世道，跪着挣最省事。"
不要自己加语言标签，不要加角色名，后端会拼接。
不要写任何括号、方括号、标签符号。

## 角色设定要求

- 2 个角色，code 分别是 "S1" 和 "S2"
- appearance 要写清年龄、族裔、发型、眼神、面部特征
- wardrobe 要写清服装细节
- persona 要写性格与说话方式
- voice_profile 要写声线特征

## 创作方向

视觉参考：%s
情绪基调：%s

## JSON 结构

{
  "title": "片名",
  "logline": "一句话故事",
  "style_anchor": {
    "film_reference": "参考影片的布光风格",
    "color_scheme": "色调方案",
    "lighting_strategy": "光位策略，比如把两种色温分给两个角色",
    "mood": "情绪基调"
  },
  "characters": [
    {
      "code": "S1",
      "name": "角色名",
      "age_range": "年龄段",
      "appearance": "外形锚点",
      "wardrobe": "服装",
      "persona": "性格与说话方式",
      "voice_profile": "声线特征"
    }
  ],
  "scenes": [
    {
      "code": "SC1",
      "name": "场景名",
      "location": "地点细节",
      "time_of_day": "时间",
      "lighting": "光位描述",
      "color_temp": "色温策略",
      "props": ["道具1", "道具2", "道具3"]
    }
  ],
  "shots": [
    {
      "shot_code": "Shot 1",
      "scene_code": "SC1",
      "duration": 5.0,
      "shot_size": "景别",
      "camera_height": "机位高度",
      "camera_move": "单一运镜",
      "visual_prompt": "技术化的画面描述，包含光线、构图、人物动作、道具",
      "speaker_code": "S1",
      "dialogue": "台词原文，不带标签",
      "soundscape": "环境音与动作音",
      "music_cue": "音乐走向"
    }
  ]
}`, shotCount, p.TotalSeconds, scaleGuide, shotCount, shotCount, DialogueMax, aspectDesc, filmRef, mood)
}

// ══════════════════ 工具 ══════════════════

func (s *Service) getProjectRaw(id string) (*model.Project, error) {
	var p model.Project
	err := s.db.QueryRow(`
		SELECT id,user_id,title,idea,aspect,total_seconds,status,
		       budget_credits,spent_credits,COALESCE(keyvisual_url,'')
		FROM projects WHERE id=$1`, id).
		Scan(&p.ID, &p.UserID, &p.Title, &p.Idea, &p.Aspect, &p.TotalSeconds, &p.Status,
			&p.BudgetCredits, &p.SpentCredits, &p.KeyvisualURL)
	if err != nil {
		return nil, errors.New("项目不存在")
	}
	return &p, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func getenvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

var _ = log.Println
var _ = bytes.NewBuffer
