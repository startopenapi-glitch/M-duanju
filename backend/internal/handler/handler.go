package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"drama-platform/internal/model"
	"drama-platform/internal/partner"
	"drama-platform/internal/service"
)

type Handler struct {
	svc       *service.Service
	jwtSecret string
}

func New(svc *service.Service, secret string) *Handler {
	return &Handler{svc: svc, jwtSecret: secret}
}

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": data})
}

func fail(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"ok": false, "error": msg})
}

// ══════════════════ 认证 ══════════════════

func (h *Handler) Register(c *gin.Context) {
	var req model.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误："+err.Error())
		return
	}
	// 归属只认服务端签名过的候选 cookie；body 里的 partnerCode 永远不采信。
	if raw, ce := c.Cookie(partner.CandidateCookie); ce == nil && raw != "" {
		if code, ve := h.svc.PartnerVerifyCandidate(raw); ve == nil {
			req.PartnerCode = code
		}
	}
	u, err := h.svc.CreateUser(&req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	h.svc.Log(u.ID, "", "", "register", "", "", c.ClientIP())
	token := h.issueToken(u)
	ok(c, model.LoginResp{Token: token, User: u})
}

func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Email) == "" || req.Password == "" {
		fail(c, 400, "参数错误")
		return
	}
	u, err := h.svc.AuthenticatePassword(req.Email, req.Password, req.TOTPCode, c.ClientIP())
	if err != nil {
		authFailure(c, err)
		return
	}
	// 登录成功即清理来源候选：已有账号不自动绑定、不改变原归属。
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(partner.CandidateCookie, "", -1, "/", "", true, true)
	ok(c, model.LoginResp{Token: h.issueToken(u), User: u})
}

func (h *Handler) Me(c *gin.Context) {
	uid := c.GetString("userID")
	u, err := h.svc.GetUser(uid)
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, u)
}

// RequestEmailCode 发送邮箱登录验证码
func (h *Handler) RequestEmailCode(c *gin.Context) {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.svc.RequestLoginCode(req.Email, c.ClientIP()); err != nil {
		// 频率限制用 429；邮件服务未配置/发送失败属于服务端问题，用 503，别让用户误以为是自己发太快
		code := 503
		if strings.Contains(err.Error(), "频繁") {
			code = 429
		}
		fail(c, code, err.Error())
		return
	}
	ok(c, gin.H{"sent": true, "message": "验证码已发送，10 分钟内有效"})
}

// LoginByEmailCode 用邮箱验证码登录
func (h *Handler) LoginByEmailCode(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		TOTPCode string `json:"totpCode"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Email) == "" || req.Code == "" {
		fail(c, 400, "参数错误")
		return
	}
	u, err := h.svc.AuthenticateCode(req.Email, req.Code, req.TOTPCode, c.ClientIP())
	if err != nil {
		authFailure(c, err)
		return
	}
	// 登录成功即清理来源候选：已有账号不自动绑定、不改变原归属。
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(partner.CandidateCookie, "", -1, "/", "", true, true)
	ok(c, model.LoginResp{Token: h.issueToken(u), User: u})
}

func authFailure(c *gin.Context, err error) {
	if errors.Is(err, service.ErrNeedTOTP) || errors.Is(err, service.ErrInvalidTOTP) {
		c.AbortWithStatusJSON(401, gin.H{"ok": false, "needTotp": true, "error": err.Error()})
		return
	}
	status := 401
	if errors.Is(err, service.ErrAuthRateLimited) {
		status = 429
	}
	if errors.Is(err, service.ErrAuthUnavailable) {
		status = 503
	}
	fail(c, status, err.Error())
}

func (h *Handler) issueToken(u *model.User) string {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  u.ID,
		"role": u.Role,
		"ver":  u.SessionVersion,
		"exp":  jwt.NewNumericDate(nowPlus(7 * 24 * 60 * 60)),
	})
	s, _ := t.SignedString([]byte(h.jwtSecret))
	return s
}

// ══════════════════ 项目 ══════════════════

func (h *Handler) CreateProject(c *gin.Context) {
	uid := c.GetString("userID")
	var req model.CreateProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"ok": false, "code": "project_not_created", "error": "参数错误：" + err.Error()})
		return
	}
	if req.Title == "" {
		req.Title = "未命名短片"
	}
	p, err := h.svc.CreateProject(uid, &req)
	if err != nil {
		projectCreationError(c, err)
		return
	}
	ok(c, p)
}

func projectCreationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrProjectCreationInvalid):
		c.AbortWithStatusJSON(400, gin.H{"ok": false, "code": "project_not_created", "error": err.Error()})
	case errors.Is(err, service.ErrProjectCreationConflict):
		c.AbortWithStatusJSON(409, gin.H{"ok": false, "code": "project_creation_conflict", "error": err.Error()})
	default:
		fail(c, 500, "创建结果暂未确认，请核对并恢复原请求")
	}
}
func (h *Handler) CreationContext(c *gin.Context) {
	p, err := h.svc.CreationContext(c.GetString("userID"), c.Query("requestKey"))
	if err != nil {
		projectCreationError(c, err)
		return
	}
	ok(c, p)
}

func (h *Handler) GetProject(c *gin.Context) {
	uid := c.GetString("userID")
	role := c.GetString("role")
	p, err := h.svc.GetProject(c.Param("id"))
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	if role != "admin" && p.UserID != uid {
		fail(c, 403, "无权访问")
		return
	}
	ok(c, p)
}

func (h *Handler) ListProjects(c *gin.Context) {
	uid := c.GetString("userID")
	page := atoiDefault(c.Query("page"), 1)
	size := atoiDefault(c.Query("pageSize"), 20)
	c.Header("Cache-Control", "private, no-store")
	resp, err := h.svc.ListProjectsFiltered(uid, page, size, service.ProjectListFilter{Query: c.Query("q"), Status: c.Query("status")})
	if err != nil {
		if err == service.ErrProjectFilter {
			fail(c, 400, err.Error())
		} else {
			fail(c, 500, "项目列表暂不可用")
		}
		return
	}
	ok(c, resp)
}

func (h *Handler) DeleteProject(c *gin.Context) {
	force := c.Query("force") == "true"
	err := h.svc.DeleteProject(c.GetString("userID"), c.Param("id"), force)
	switch {
	case errors.Is(err, service.ErrProjectBusy):
		fail(c, 409, err.Error())
	case errors.Is(err, service.ErrProjectNotFound):
		fail(c, 404, err.Error())
	case err != nil:
		fail(c, 400, err.Error())
	default:
		ok(c, gin.H{"deleted": true})
	}
}

func (h *Handler) UpdateProject(c *gin.Context) {
	var req model.UpdateProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.svc.UpdateProject(c.Param("id"), &req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"updated": true})
}

// ══════════════════ 剧本 ══════════════════

func (h *Handler) GenerateScript(c *gin.Context) {
	uid := c.GetString("userID")
	id := c.Param("id")
	p, err := h.svc.GetProject(id)
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	if p.UserID != uid && c.GetString("role") != "admin" {
		fail(c, 403, "无权访问")
		return
	}

	var body map[string]string
	c.ShouldBindJSON(&body)
	if body == nil {
		body = map[string]string{}
	}

	script, warnings, err := h.svc.GenerateScript(id, body)
	if err != nil {
		fail(c, 422, err.Error())
		return
	}
	_ = warnings
	ok(c, gin.H{"script": script})
}

func (h *Handler) UpdateShot(c *gin.Context) {
	var req model.UpdateShotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	// 红线校验：台词
	if d := strings.TrimSpace(req.Dialogue); d != "" {
		if len([]rune(d)) > service.DialogueMax {
			fail(c, 400, "台词超过 "+itoa(service.DialogueMax)+" 字上限（口型会失准）")
			return
		}
		if containsAny(d, []string{"<d>", "[Chinese]", "（", "(", "["}) {
			fail(c, 400, "台词不能混入标签或括号，只能写原文")
			return
		}
	}
	if err := h.svc.UpdateShot(c.Param("id"), c.Param("sid"), &req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"updated": true})
}

func (h *Handler) UpdateCharacter(c *gin.Context) {
	var req model.UpdateCharacterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.svc.UpdateCharacter(c.Param("id"), c.Param("cid"), &req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"updated": true})
}

func (h *Handler) UpdateScene(c *gin.Context) {
	var req model.UpdateSceneReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.svc.UpdateScene(c.Param("id"), c.Param("sid"), &req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"updated": true})
}

// ══════════════════ 生成 ══════════════════

func manualStageLocked(p *model.Project, allowed ...string) bool {
	if p == nil || p.WorkflowMode != "manual" {
		return false
	}
	for _, status := range allowed {
		if p.Status == status {
			return false
		}
	}
	return true
}

func (h *Handler) QueueKeyVisual(c *gin.Context) {
	uid := c.GetString("userID")
	id := c.Param("id")
	p, _ := h.svc.GetProject(id)
	if manualStageLocked(p, "SCRIPT_REVIEW", "KEYVISUAL_REVIEW", "FAILED") {
		fail(c, 409, "请先确认剧本，当前阶段不能生成定妆照")
		return
	}
	// 只入队，由独立 worker 进程执行（API 重启不影响任务）
	if _, err := h.svc.EnqueueJob(service.JobKeyVisual, id, "", 0, ""); err != nil {
		generationFail(c, err)
		return
	}
	// Queue transaction owns the status transition.
	ok(c, gin.H{"queued": true, "userId": uid})
}

func (h *Handler) ConfirmScript(c *gin.Context) {
	id := c.Param("id")
	jobID, err := h.svc.ConfirmScript(c.GetString("userID"), id)
	if err != nil {
		generationFail(c, err)
		return
	}
	ok(c, gin.H{"confirmed": true, "jobId": jobID})
}

func (h *Handler) ConfirmKeyVisual(c *gin.Context) {
	id := c.Param("id")
	n, err := h.svc.ConfirmKeyVisual(c.GetString("userID"), id)
	if err != nil {
		generationFail(c, err)
		return
	}
	ok(c, gin.H{"confirmed": true, "shots": n})
}

func (h *Handler) ApproveKeyVisual(c *gin.Context) {
	// Compatibility alias: old clients now get the same real confirmation
	// transition as /keyvisual/confirm (which used to be a no-op).
	h.ConfirmKeyVisual(c)
}

func (h *Handler) QueueShots(c *gin.Context) {
	uid := c.GetString("projectOwnerID")
	if uid == "" {
		uid = c.GetString("userID")
	}
	id := c.Param("id")
	var body struct {
		Seqs []int `json:"seqs"`
	}
	c.ShouldBindJSON(&body)
	p, _ := h.svc.GetProject(id)
	if manualStageLocked(p, "KEYVISUAL_REVIEW", "SHOTS_REVIEW", "FAILED") {
		fail(c, 409, "请先确认定妆照，当前阶段不能生成镜头")
		return
	}
	n, err := h.svc.GenerateShots(id, uid, body.Seqs)
	if err != nil {
		generationFail(c, err)
		return
	}
	ok(c, gin.H{"submitted": n})
}

func (h *Handler) PollShots(c *gin.Context) {
	h.svc.PollShots(c.Param("id"))
	ok(c, gin.H{"polled": true})
}

func (h *Handler) ApproveShot(c *gin.Context) {
	if err := h.svc.ApproveShot(c.Param("id"), atoiDefault(c.Param("seq"), -1)); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"approved": true})
}

func (h *Handler) UnapproveShot(c *gin.Context) {
	if err := h.svc.UnapproveShot(c.Param("id"), atoiDefault(c.Param("seq"), -1)); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"unapproved": true})
}

func (h *Handler) RegenerateShot(c *gin.Context) {
	var in struct {
		OverrideLimit bool `json:"overrideLimit"`
	}
	_ = c.ShouldBindJSON(&in)
	if err := h.svc.RegenerateShot(c.Param("id"), atoiDefault(c.Param("seq"), -1), in.OverrideLimit); err != nil {
		generationFail(c, err)
		return
	}
	ok(c, gin.H{"regenerated": true})
}

func (h *Handler) Assemble(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.svc.EnqueueJob(service.JobAssemble, id, "", 0, ""); err != nil {
		generationFail(c, err)
		return
	}
	ok(c, gin.H{"assembling": true})
}

// ══════════════════ 积分 / 订单 ══════════════════

func (h *Handler) GetBalance(c *gin.Context) {
	uid := c.GetString("userID")
	bal, txs, err := h.svc.GetBalance(uid)
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, gin.H{"balance": bal, "transactions": txs})
}

func (h *Handler) CreateOrder(c *gin.Context) {
	uid := c.GetString("userID")
	var req model.CreateOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误："+err.Error())
		return
	}
	o, err := h.svc.CreateOrder(uid, &req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, o)
}

func (h *Handler) ListMyOrders(c *gin.Context) {
	uid := c.GetString("userID")
	page := atoiDefault(c.Query("page"), 1)
	size := atoiDefault(c.Query("pageSize"), 20)
	resp, err := h.svc.ListOrders(uid, c.Query("status"), page, size)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, resp)
}

// ══════════════════ 管理后台 ══════════════════

func (h *Handler) AdminListOrders(c *gin.Context) {
	v, e := h.svc.AdminOrders(c.Query("kw"), c.Query("status"), atoiDefault(c.Query("page"), 1), atoiDefault(c.Query("pageSize"), 20))
	adminResult(c, v, e)
}

func (h *Handler) AdminHandleOrder(c *gin.Context) {
	adminID := c.GetString("userID")
	var req model.HandleOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.svc.HandleOrder(adminID, c.Param("id"), &req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"handled": true})
}

func (h *Handler) AdminAdjustCredits(c *gin.Context) {
	adminID := c.GetString("userID")
	var req model.AdjustCreditsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误："+err.Error())
		return
	}
	e := h.svc.AdjustCredits(adminID, &req)
	adminResult(c, gin.H{"adjusted": true}, e)
}

func (h *Handler) AdminStats(c *gin.Context) {
	stats := h.svc.AdminStats()
	ok(c, stats)
}

func (h *Handler) AdminListUsers(c *gin.Context) {
	page := atoiDefault(c.Query("page"), 1)
	size := atoiDefault(c.Query("pageSize"), 20)
	kw := c.Query("kw")
	resp, err := h.svc.ListUsers(kw, page, size)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, resp)
}

var _ = errors.New
var _ = uuid.New

func generationFail(c *gin.Context, err error) {
	var preflight *service.QueuePreflightError
	if errors.As(err, &preflight) {
		c.AbortWithStatusJSON(429, gin.H{"ok": false, "error": err.Error(), "code": preflight.Code, "requiredCredits": preflight.Required, "availableCredits": preflight.Available})
		return
	}
	code := "generation_rejected"
	if errors.Is(err, service.ErrSubmissionUnknown) {
		code = "submission_unknown"
	}
	c.AbortWithStatusJSON(409, gin.H{"ok": false, "error": err.Error(), "code": code})
}
func (h *Handler) ResumeGenerationJob(c *gin.Context) {
	if err := h.svc.ResumeJob(c.Param("id"), c.Param("jid")); err != nil {
		generationFail(c, err)
		return
	}
	ok(c, gin.H{"queued": true})
}

// PartnerPublicProfile 推广页公开接口：只返回名称/简介/客服微信，并下发签名候选 cookie。
// 含候选信息的响应禁止共享缓存。
func (h *Handler) PartnerPublicProfile(c *gin.Context) {
	code := c.Param("code")
	p, err := h.svc.PartnerPublicProfile(code)
	if err != nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(404, gin.H{"error": "推广链接不存在"})
		return
	}
	c.Header("Cache-Control", "no-store")
	if p.Usable {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(partner.CandidateCookie, h.svc.PartnerSignCandidate(code),
			int(partner.CandidateTTL.Seconds()), "/", "", true, true)
	}
	ok(c, p)
}
