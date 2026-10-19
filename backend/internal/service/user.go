package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"context"
	"drama-platform/internal/model"
	"drama-platform/internal/partner"
)

// ══════════════════ 用户 ══════════════════

func (s *Service) CreateUser(req *model.RegisterReq) (*model.User, error) {
	req.Email = normalizeEmail(req.Email)
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	nick := req.Nickname
	if nick == "" {
		nick = req.Email[:min(len(req.Email), 12)]
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize registrations for the canonical mailbox without rewriting legacy accounts.
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "register:"+req.Email); err != nil {
		return nil, err
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE lower(btrim(email))=$1)`, req.Email).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("该邮箱已注册")
	}
	var u model.User
	err = tx.QueryRow(`
		INSERT INTO users (email, password_hash, nickname, role, credits)
		VALUES ($1,$2,$3,'user',0)
		RETURNING id,email,password_hash,nickname,role,credits,status,created_at,updated_at,session_version`,
		req.Email, string(hash), nick,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Nickname, &u.Role, &u.Credits, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.SessionVersion)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, errors.New("该邮箱已注册")
		}
		return nil, err
	}
	// 推广来源绑定：与建号同事务。来源无效/为空不阻断注册，代理暂停不写归属；
	// 数据库异常必须让整体回滚，不能伪装绑定成功。
	if req.PartnerCode != "" {
		if e := s.partner.BindOnRegister(context.Background(), tx, u.ID, req.PartnerCode); e != nil && !errors.Is(e, partner.ErrSuspended) {
			return nil, e
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// 异步发欢迎邮件（未配 SMTP 自动跳过）
	go s.SendWelcome(u.Email, u.Nickname)
	return &u, nil
}

func (s *Service) Authenticate(email, password string) (*model.User, error) {
	email = normalizeEmail(email)
	var u model.User
	err := s.db.QueryRow(`
		SELECT id,email,password_hash,nickname,role,credits,status,created_at,updated_at,session_version
		FROM users WHERE email=$1`, email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Nickname, &u.Role, &u.Credits, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.SessionVersion)
	if err != nil {
		return nil, errors.New("邮箱或密码错误")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, errors.New("邮箱或密码错误")
	}
	if u.Status != "active" {
		return nil, errors.New("账号已被禁用")
	}
	return &u, nil
}

func (s *Service) GetUser(id string) (*model.User, error) {
	var u model.User
	err := s.db.QueryRow(`
		SELECT id,email,password_hash,nickname,role,credits,status,created_at,updated_at,session_version
		FROM users WHERE id=$1`, id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Nickname, &u.Role, &u.Credits, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.SessionVersion)
	if err != nil {
		return nil, errors.New("用户不存在")
	}
	return &u, nil
}

// ══════════════════ 项目 ══════════════════

// ProjectOwner 轻量查项目归属，供权限中间件使用（不加载关联数据）
func (s *Service) ProjectOwner(id string) (string, error) {
	var uid string
	if err := s.db.QueryRow(`SELECT user_id FROM projects WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&uid); err != nil {
		return "", errors.New("项目不存在")
	}
	return uid, nil
}

func (s *Service) GetProject(id string) (*model.Project, error) {
	var p model.Project
	err := s.db.QueryRow(`
		SELECT id,user_id,title,idea,style_config,aspect,total_seconds,status,workflow_mode,
		       budget_credits,spent_credits,COALESCE(keyvisual_url,''),COALESCE(final_url,''),
		       COALESCE(error_msg,''),created_at,updated_at
		FROM projects WHERE id=$1 AND deleted_at IS NULL`, id,
	).Scan(&p.ID, &p.UserID, &p.Title, &p.Idea, &p.StyleConfig, &p.Aspect, &p.TotalSeconds,
		&p.Status, &p.WorkflowMode, &p.BudgetCredits, &p.SpentCredits, &p.KeyvisualURL, &p.FinalURL,
		&p.ErrorMsg, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, errors.New("项目不存在")
	}

	jobs, e := s.ListGenerationJobs(id)
	if e != nil {
		return nil, e
	}
	p.GenerationJobs, e = json.Marshal(jobs)
	if e != nil {
		return nil, e
	}
	if e = s.db.QueryRow(`SELECT COALESCE(sum(reserved_credits),0) FROM jobs WHERE project_id=$1`, id).Scan(&p.ReservedCredits); e != nil {
		return nil, e
	}
	var err2 error
	p.Characters, err2 = s.listCharacters(id)
	if err2 != nil {
		return nil, err2
	}
	p.Scenes, err2 = s.listScenes(id)
	if err2 != nil {
		return nil, err2
	}
	p.Shots, err2 = s.listShots(id)
	if err2 != nil {
		return nil, err2
	}
	p.Reviews = s.listAudit(id, 30)
	// 前端渲染依赖的元信息（缺失会导致 undefined 访问崩溃）
	p.Meta = &model.ProjectMeta{
		ShotCount:    ShotCount,
		DialogueMax:  DialogueMax,
		RetryLimit:   ShotMaxRetry,
		Aspect:       p.Aspect,
		TotalSeconds: p.TotalSeconds,
	}

	// 组装帧
	for i := range p.Shots {
		p.Shots[i].RetryLimit = 2
		p.Shots[i].ChargeYuan = float64(p.Shots[i].ChargeMicros) / 1e6
		frames, _ := s.listFrames(p.Shots[i].ID)
		p.Shots[i].Frames = frames
	}
	return &p, nil
}

func (s *Service) ListProjects(userID string, page, pageSize int) (*model.PageResp, error) {
	return s.ListProjectsFiltered(userID, page, pageSize, ProjectListFilter{})
}

func (s *Service) UpdateProject(id string, req *model.UpdateProjectReq) error {
	tx, err := s.beginProjectEdit(id, req.Title != "" || len(req.StyleConfig) > 0)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var style interface{}
	if len(req.StyleConfig) > 0 {
		style = string(req.StyleConfig)
	}
	// budgetYuan 字段保留兼容，但不再接受客户端改预算。
	res, err := tx.Exec(`UPDATE projects SET
 title=COALESCE(NULLIF($1,''),title),style_config=COALESCE($2::jsonb,style_config),updated_at=now() WHERE id=$3`, req.Title, style, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("项目不存在")
	}
	return tx.Commit()
}

// ══════════════════ 角色 / 场景 / 分镜 ══════════════════

func (s *Service) listCharacters(projectID string) ([]model.Character, error) {
	rows, err := s.db.Query(`
		SELECT id,project_id,code,name,age_range,appearance,wardrobe,persona,voice_profile,sort_order
		FROM characters WHERE project_id=$1 ORDER BY sort_order`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Character{}
	for rows.Next() {
		var c model.Character
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Code, &c.Name, &c.AgeRange,
			&c.Appearance, &c.Wardrobe, &c.Persona, &c.VoiceProfile, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) listScenes(projectID string) ([]model.Scene, error) {
	rows, err := s.db.Query(`
		SELECT id,project_id,code,name,location,time_of_day,lighting,color_temp,props
		FROM scenes WHERE project_id=$1 ORDER BY code`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Scene{}
	for rows.Next() {
		var sc model.Scene
		var propsRaw []byte
		if err := rows.Scan(&sc.ID, &sc.ProjectID, &sc.Code, &sc.Name, &sc.Location,
			&sc.TimeOfDay, &sc.Lighting, &sc.ColorTemp, &propsRaw); err != nil {
			return nil, err
		}
		sc.Props = decodeStringArray(propsRaw)
		out = append(out, sc)
	}
	return out, nil
}

func (s *Service) listShots(projectID string) ([]model.Shot, error) {
	rows, err := s.db.Query(`
		SELECT id,project_id,COALESCE(scene_id,''),shot_code,seq,duration,shot_size,camera_height,
		       camera_move,visual_prompt,COALESCE(speaker_code,''),COALESCE(dialogue,''),
		       soundscape,music_cue,status,retry_count,COALESCE(upstream_id,''),
		       COALESCE(video_url,''),COALESCE(duration_actual,0),COALESCE(width,0),COALESCE(height,0),
		       COALESCE(charge_micros,0),COALESCE(error_msg,'')
		FROM shots WHERE project_id=$1 ORDER BY seq`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Shot{}
	for rows.Next() {
		var sh model.Shot
		if err := rows.Scan(&sh.ID, &sh.ProjectID, &sh.SceneID, &sh.ShotCode, &sh.Seq, &sh.Duration,
			&sh.ShotSize, &sh.CameraHeight, &sh.CameraMove, &sh.VisualPrompt, &sh.SpeakerCode,
			&sh.Dialogue, &sh.Soundscape, &sh.MusicCue, &sh.Status, &sh.RetryCount, &sh.UpstreamID,
			&sh.VideoURL, &sh.DurationActual, &sh.Width, &sh.Height, &sh.ChargeMicros, &sh.ErrorMsg); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, nil
}

func (s *Service) listFrames(shotID string) ([]string, error) {
	// ★ 永远返回非 nil 切片：nil 会被 JSON 序列化成 null，前端访问 .length 会崩
	out := []string{}
	rows, err := s.db.Query(`SELECT path FROM shot_frames WHERE shot_id=$1 ORDER BY created_at`, shotID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out = append(out, p)
	}
	return out, nil
}

func (s *Service) UpdateShot(projectID, id string, req *model.UpdateShotReq) error {
	tx, err := s.beginProjectEdit(projectID, true)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE shots SET
		  shot_size=COALESCE(NULLIF($1,''),shot_size),
		  camera_height=COALESCE(NULLIF($2,''),camera_height),
		  camera_move=COALESCE(NULLIF($3,''),camera_move),
		  visual_prompt=COALESCE(NULLIF($4,''),visual_prompt),
		  speaker_code=COALESCE(NULLIF($5,''),speaker_code),
		  dialogue=COALESCE($6,dialogue),
		  soundscape=COALESCE(NULLIF($7,''),soundscape),
		  music_cue=COALESCE(NULLIF($8,''),music_cue),
		  updated_at=now()
		WHERE id=$9 AND project_id=$10`,
		req.ShotSize, req.CameraHeight, req.CameraMove, req.VisualPrompt,
		req.SpeakerCode, nullIfEmpty(req.Dialogue), req.Soundscape, req.MusicCue, id, projectID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("子资源不属于该项目或不存在")
	}
	return tx.Commit()
}

func (s *Service) UpdateCharacter(projectID, id string, req *model.UpdateCharacterReq) error {
	tx, err := s.beginProjectEdit(projectID, true)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE characters SET
		  name=COALESCE(NULLIF($1,''),name),
		  age_range=COALESCE(NULLIF($2,''),age_range),
		  appearance=COALESCE(NULLIF($3,''),appearance),
		  wardrobe=COALESCE(NULLIF($4,''),wardrobe),
		  persona=COALESCE(NULLIF($5,''),persona),
		  voice_profile=COALESCE(NULLIF($6,''),voice_profile)
		WHERE id=$7 AND project_id=$8`,
		req.Name, req.AgeRange, req.Appearance, req.Wardrobe, req.Persona, req.VoiceProfile, id, projectID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("子资源不属于该项目或不存在")
	}
	return tx.Commit()
}

func (s *Service) UpdateScene(projectID, id string, req *model.UpdateSceneReq) error {
	tx, err := s.beginProjectEdit(projectID, true)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// JSONB 列必须传 JSON 字符串并显式转型，直接传 []string 会编码失败
	var propsVal interface{}
	if len(req.Props) > 0 {
		b, _ := json.Marshal(req.Props)
		propsVal = string(b)
	}
	result, err := tx.Exec(`
		UPDATE scenes SET
		  name=COALESCE(NULLIF($1,''),name),
		  location=COALESCE(NULLIF($2,''),location),
		  time_of_day=COALESCE(NULLIF($3,''),time_of_day),
		  lighting=COALESCE(NULLIF($4,''),lighting),
		  color_temp=COALESCE(NULLIF($5,''),color_temp),
		  props=COALESCE($6::jsonb, props)
		WHERE id=$7 AND project_id=$8`,
		req.Name, req.Location, req.TimeOfDay, req.Lighting, req.ColorTemp, propsVal, id, projectID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("子资源不属于该项目或不存在")
	}
	return tx.Commit()
}

// ══════════════════ 日志 ══════════════════

func (s *Service) Log(userID, projectID, shotID, action, from, to, note string) {
	s.db.Exec(`
		INSERT INTO audit_logs (user_id,project_id,shot_id,action,from_status,to_status,note)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		nullIfEmpty(userID), nullIfEmpty(projectID), nullIfEmpty(shotID), action,
		nullIfEmpty(from), nullIfEmpty(to), note)
}

func (s *Service) listAudit(projectID string, limit int) []model.AuditLog {
	rows, err := s.db.Query(`
		SELECT id,action,COALESCE(from_status,''),COALESCE(to_status,''),COALESCE(note,''),created_at
		FROM audit_logs WHERE project_id=$1 ORDER BY created_at DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []model.AuditLog{}
	for rows.Next() {
		var a model.AuditLog
		if err := rows.Scan(&a.ID, &a.Action, &a.FromStatus, &a.ToStatus, &a.Note, &a.CreatedAt); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

// ══════════════════ 积分 ══════════════════

func (s *Service) GetBalance(userID string) (int64, []model.CreditTransaction, error) {
	var bal int64
	if err := s.db.QueryRow(`SELECT credits FROM users WHERE id=$1`, userID).Scan(&bal); err != nil {
		return 0, nil, errors.New("用户不存在")
	}
	rows, err := s.db.Query(`
		SELECT id,amount,balance_after,kind,COALESCE(project_id,''),COALESCE(note,''),created_at
		FROM credit_transactions WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return bal, nil, err
	}
	defer rows.Close()
	txs := []model.CreditTransaction{}
	for rows.Next() {
		var t model.CreditTransaction
		if err := rows.Scan(&t.ID, &t.Amount, &t.BalanceAfter, &t.Kind, &t.ProjectID, &t.Note, &t.CreatedAt); err != nil {
			continue
		}
		txs = append(txs, t)
	}
	return bal, txs, nil
}

// AdjustCredits 管理员调整积分（原子操作）
// SpendCredits 扣费（返回是否成功）
func (s *Service) SpendCredits(userID, projectID string, amount int64, note string) error {
	if amount <= 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var bal int64
	if err := tx.QueryRow(`SELECT credits FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&bal); err != nil {
		return errors.New("用户不存在")
	}
	if bal < amount {
		return fmt.Errorf("积分不足：需要 %d，当前 %d", amount, bal)
	}
	newBal := bal - amount
	if _, err := tx.Exec(`UPDATE users SET credits=$1, updated_at=now() WHERE id=$2`, newBal, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO credit_transactions (user_id,amount,balance_after,kind,project_id,note)
		VALUES ($1,$2,$3,'spend',$4,$5)`,
		userID, -amount, newBal, nullIfEmpty(projectID), note); err != nil {
		return err
	}
	// 同步项目已花费
	tx.Exec(`UPDATE projects SET spent_credits=spent_credits+$1, updated_at=now() WHERE id=$2`, amount, projectID)
	return tx.Commit()
}

// ══════════════════ 充值订单 ══════════════════

func (s *Service) CreateOrder(userID string, req *model.CreateOrderReq) (*model.RechargeOrder, error) {
	runID, err := s.resolveRechargeRun(userID, req.RunID)
	if err != nil {
		return nil, err
	}
	amountCents := int64(req.AmountYuan*100 + 0.5)
	if amountCents <= 0 {
		return nil, errors.New("缺少金额")
	}
	credits := rechargeCredits(amountCents)
	var o model.RechargeOrder
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`
		INSERT INTO recharge_orders (user_id,amount_yuan,amount_cents,credits,channel,run_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id,user_id,amount_yuan,credits,status,created_at,COALESCE(run_id,'')`,
		userID, req.AmountYuan, amountCents, credits, req.Channel, nullIfEmpty(runID),
	).Scan(&o.ID, &o.UserID, &o.AmountYuan, &o.Credits, &o.Status, &o.CreatedAt, &o.RunID)
	if err != nil {
		return nil, err
	}
	// 人工充值入口同样在同事务写入来源快照；失败则整单回滚。
	if e := s.partner.SnapshotOrder(context.Background(), tx, o.ID, userID); e != nil {
		return nil, e
	}
	if e := tx.Commit(); e != nil {
		return nil, e
	}
	return &o, nil
}

func (s *Service) ListOrders(userID, status string, page, pageSize int) (*model.PageResp, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var conds []string
	args := []interface{}{}
	if userID != "" {
		args = append(args, userID)
		conds = append(conds, fmt.Sprintf("user_id=$%d", len(args)))
	}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("status=$%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	s.db.QueryRow(`SELECT count(*) FROM recharge_orders`+where, args...).Scan(&total)

	q := `SELECT id,user_id,amount_yuan,credits,status,COALESCE(channel,''),COALESCE(admin_note,''),
	      COALESCE(handled_by,''),created_at,COALESCE(pay_mode,'manual') FROM recharge_orders` + where +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*model.RechargeOrder{}
	for rows.Next() {
		var o model.RechargeOrder
		if err := rows.Scan(&o.ID, &o.UserID, &o.AmountYuan, &o.Credits, &o.Status,
			&o.Channel, &o.AdminNote, &o.HandledBy, &o.CreatedAt, &o.PayMode); err != nil {
			continue
		}
		items = append(items, &o)
	}
	return &model.PageResp{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// HandleOrder 管理员审核充值订单
func (s *Service) HandleOrder(adminID, orderID string, req *model.HandleOrderReq) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var o model.RechargeOrder
	err = tx.QueryRow(`SELECT id,user_id,credits,status,COALESCE(run_id,''),COALESCE(channel,''),COALESCE(out_trade_no,'') FROM recharge_orders WHERE id=$1 FOR UPDATE`, orderID).
		Scan(&o.ID, &o.UserID, &o.Credits, &o.Status, &o.RunID, &o.Channel, &o.OutTradeNo)
	if err != nil {
		return errors.New("订单不存在")
	}
	// Only genuine manual orders may be approved here; channel truth comes from query/notify.
	if o.OutTradeNo != "" || (o.Channel != "" && o.Channel != "manual") {
		return errors.New("渠道订单必须通过支付渠道核验")
	}
	if o.Status != "pending" {
		return fmt.Errorf("订单已处理（%s）", o.Status)
	}

	if !req.Approve {
		if _, err := tx.Exec(`UPDATE recharge_orders SET status='rejected',admin_note=$1,handled_by=$2,handled_at=now() WHERE id=$3`, req.Note, adminID, orderID); err != nil {
			return err
		}
		if err := adminAuditTx(tx, adminID, "manual_order_reject", orderID, "pending", "rejected", req.Note); err != nil {
			return err
		}
		return tx.Commit()
	}

	// 批准：余额、流水、订单入账标识和续跑唤醒必须同一事务，任一步失败整单回滚。
	var newBal int64
	if err := tx.QueryRow(`UPDATE users SET credits=credits+$1, updated_at=now() WHERE id=$2 RETURNING credits`,
		o.Credits, o.UserID).Scan(&newBal); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO credit_transactions (user_id,amount,balance_after,kind,note,operator_id)
	         VALUES ($1,$2,$3,'recharge',$4,$5)`, o.UserID, o.Credits, newBal, req.Note, adminID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE recharge_orders SET status='paid',credited=true,paid_at=now(),admin_note=$1,handled_by=$2,handled_at=now(),updated_at=now() WHERE id=$3`,
		req.Note, adminID, orderID); err != nil {
		return err
	}
	if o.RunID != "" {
		// 只唤醒属于该订单用户的执行；坏绑定不得让正常入账失败。
		var runOwner string
		if e := tx.QueryRow(`SELECT user_id FROM generation_runs WHERE id=$1`, o.RunID).Scan(&runOwner); e == nil && runOwner == o.UserID {
			if e := s.runstore.EnqueueInTx(context.Background(), tx, o.RunID, orderID, o.UserID); e != nil {
				return e
			}
		}
	}
	if err := adminAuditTx(tx, adminID, "manual_order_approve", orderID, "pending", "paid", req.Note); err != nil {
		return err
	}
	return tx.Commit()
}

// ══════════════════ 工具 ══════════════════

func isUniqueViolation(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func propsOrNil(p []string) interface{} {
	if len(p) == 0 {
		return nil
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// decodeStringArray 把 JSONB 列的原始字节解成 []string
// pgx 对 JSONB 返回 []uint8，直接 Scan 到 []string 会报
// "unsupported Scan, storing driver.Value type []uint8 into type *[]string"
func decodeStringArray(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func uuidOrNil(s string) interface{} {
	if s == "" {
		return nil
	}
	if _, err := uuid.Parse(s); err != nil {
		return nil
	}
	return s
}

var _ = pgx.ErrNoRows
var _ = sql.ErrNoRows
