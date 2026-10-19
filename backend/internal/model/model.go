package model

import (
	"encoding/json"
	"time"
)

// ─────────── 用户 ───────────

type User struct {
	SessionVersion int64     `json:"-"`
	ID             string    `json:"id" db:"id"`
	Email          string    `json:"email" db:"email"`
	PasswordHash   string    `json:"-" db:"password_hash"`
	Nickname       string    `json:"nickname" db:"nickname"`
	Role           string    `json:"role" db:"role"`
	Credits        int64     `json:"credits" db:"credits"`
	Status         string    `json:"status" db:"status"`
	CreatedAt      time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time `json:"updatedAt" db:"updated_at"`
}

type RegisterReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=64"`
	Nickname string `json:"nickname" binding:"max=64"`
	// 由服务端从已签名的来源候选 cookie 填入；不接受客户端提交，避免 URL 参数伪造归属。
	PartnerCode string `json:"-"`
}

type LoginReq struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
	TOTPCode string `json:"totpCode"` // 启用双因素时必填
}

type ChangePasswordReq struct {
	OldPassword string `json:"oldPassword" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required,min=8,max=64"`
}

type TOTPVerifyReq struct {
	Code string `json:"code" binding:"required,len=6"`
}

type TOTPDisableReq struct {
	Password string `json:"password" binding:"required"`
}

type LoginResp struct {
	Token    string `json:"token"`
	User     *User  `json:"user"`
	NeedTOTP bool   `json:"needTotp,omitempty"`
}

// 用户敏感字段不外泄
type UserSecurity struct {
	TOTPEnabled   bool   `json:"totpEnabled"`
	PasswordSetAt string `json:"passwordSetAt,omitempty"`
	LastLoginAt   string `json:"lastLoginAt,omitempty"`
}

// ─────────── 项目 ───────────

type Project struct {
	ReservedCredits int64           `json:"reservedCredits"`
	GenerationJobs  json.RawMessage `json:"generationJobs"`
	ID              string          `json:"id" db:"id"`
	UserID          string          `json:"userId" db:"user_id"`
	Title           string          `json:"title" db:"title"`
	Idea            string          `json:"idea" db:"idea"`
	StyleConfig     []byte          `json:"styleConfig" db:"style_config"`
	Aspect          string          `json:"aspect" db:"aspect"`
	TotalSeconds    int             `json:"totalSeconds" db:"total_seconds"`
	Status          string          `json:"status" db:"status"`
	WorkflowMode    string          `json:"workflowMode" db:"workflow_mode"`
	BudgetCredits   int64           `json:"budgetCredits" db:"budget_credits"`
	SpentCredits    int64           `json:"spentCredits" db:"spent_credits"`
	KeyvisualURL    string          `json:"keyvisualUrl" db:"keyvisual_url"`
	FinalURL        string          `json:"finalUrl" db:"final_url"`
	ErrorMsg        string          `json:"errorMsg" db:"error_msg"`
	CreatedAt       time.Time       `json:"createdAt" db:"created_at"`
	UpdatedAt       time.Time       `json:"updatedAt" db:"updated_at"`

	// 关联数据
	// 集合字段不加 omitempty：空集合必须序列化为 []，
	// 否则前端拿到 undefined，访问 .map/.length 直接崩成白屏
	Characters []Character  `json:"characters"`
	Scenes     []Scene      `json:"scenes"`
	Shots      []Shot       `json:"shots"`
	Reviews    []AuditLog   `json:"reviews"`
	User       *User        `json:"user,omitempty"`
	Meta       *ProjectMeta `json:"meta,omitempty"`
}

// ProjectMeta 前端渲染所需的元信息
type ProjectMeta struct {
	ShotCount    int    `json:"shotCount"`
	DialogueMax  int    `json:"dialogueMax"`
	RetryLimit   int    `json:"retryLimit"`
	Aspect       string `json:"aspect"`
	TotalSeconds int    `json:"totalSeconds"`
}

type CreateProjectReq struct {
	IdempotencyKey string `json:"idempotencyKey" binding:"max=64"`
	Idea           string `json:"idea" binding:"required,max=500"`
	Title          string `json:"title" binding:"max=128"`
	Aspect         string `json:"aspect" binding:"required,oneof=16:9 9:16"`
	TotalSeconds   int    `json:"totalSeconds" binding:"required,min=5,max=180"`
	BudgetYuan     int64  `json:"budgetYuan"` // 已废弃：仅兼容旧客户端，服务端按范围计算上限
	FilmRef        string `json:"filmReference" binding:"max=128"`
	Mood           string `json:"mood" binding:"max=128"`
}

type UpdateProjectReq struct {
	Title       string `json:"title" binding:"max=128"`
	BudgetYuan  int64  `json:"budgetYuan" binding:"omitempty,min=5,max=2000"`
	StyleConfig []byte `json:"styleConfig"`
}

// ─────────── 角色 / 场景 / 分镜 ───────────

type Character struct {
	ID           string `json:"id" db:"id"`
	ProjectID    string `json:"projectId" db:"project_id"`
	Code         string `json:"code" db:"code"`
	Name         string `json:"name" db:"name"`
	AgeRange     string `json:"ageRange" db:"age_range"`
	Appearance   string `json:"appearance" db:"appearance"`
	Wardrobe     string `json:"wardrobe" db:"wardrobe"`
	Persona      string `json:"persona" db:"persona"`
	VoiceProfile string `json:"voiceProfile" db:"voice_profile"`
	SortOrder    int    `json:"sortOrder" db:"sort_order"`
}

type Scene struct {
	ID        string   `json:"id" db:"id"`
	ProjectID string   `json:"projectId" db:"project_id"`
	Code      string   `json:"code" db:"code"`
	Name      string   `json:"name" db:"name"`
	Location  string   `json:"location" db:"location"`
	TimeOfDay string   `json:"timeOfDay" db:"time_of_day"`
	Lighting  string   `json:"lighting" db:"lighting"`
	ColorTemp string   `json:"colorTemp" db:"color_temp"`
	Props     []string `json:"props" db:"props"`
}

type Shot struct {
	ID             string    `json:"id" db:"id"`
	ProjectID      string    `json:"projectId" db:"project_id"`
	SceneID        string    `json:"sceneId" db:"scene_id"`
	ShotCode       string    `json:"shotCode" db:"shot_code"`
	Seq            int       `json:"seq" db:"seq"`
	Duration       float64   `json:"duration" db:"duration"`
	ShotSize       string    `json:"shotSize" db:"shot_size"`
	CameraHeight   string    `json:"cameraHeight" db:"camera_height"`
	CameraMove     string    `json:"cameraMove" db:"camera_move"`
	VisualPrompt   string    `json:"visualPrompt" db:"visual_prompt"`
	SpeakerCode    string    `json:"speakerCode" db:"speaker_code"`
	Dialogue       string    `json:"dialogue" db:"dialogue"`
	Soundscape     string    `json:"soundscape" db:"soundscape"`
	MusicCue       string    `json:"musicCue" db:"music_cue"`
	Status         string    `json:"status" db:"status"`
	RetryCount     int       `json:"retryCount" db:"retry_count"`
	UpstreamID     string    `json:"upstreamId" db:"upstream_id"`
	VideoURL       string    `json:"videoUrl" db:"video_url"`
	DurationActual float64   `json:"durationActual" db:"duration_actual"`
	Width          int       `json:"width" db:"width"`
	Height         int       `json:"height" db:"height"`
	ChargeMicros   int64     `json:"chargeMicros" db:"charge_micros"`
	ErrorMsg       string    `json:"errorMsg" db:"error_msg"`
	CreatedAt      time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time `json:"updatedAt" db:"updated_at"`

	Frames     []string `json:"frames"` // 不加 omitempty：空数组必须返回 []，否则前端 undefined.length 崩溃
	RetryLimit int      `json:"retryLimit"`
	ChargeYuan float64  `json:"chargeYuan"`
}

type UpdateShotReq struct {
	ShotSize     string `json:"shotSize" binding:"max=64"`
	CameraHeight string `json:"cameraHeight" binding:"max=64"`
	CameraMove   string `json:"cameraMove" binding:"max=64"`
	VisualPrompt string `json:"visualPrompt"`
	SpeakerCode  string `json:"speakerCode" binding:"max=16"`
	Dialogue     string `json:"dialogue"`
	Soundscape   string `json:"soundscape"`
	MusicCue     string `json:"musicCue"`
}

type UpdateCharacterReq struct {
	Name         string `json:"name" binding:"max=64"`
	AgeRange     string `json:"ageRange" binding:"max=32"`
	Appearance   string `json:"appearance"`
	Wardrobe     string `json:"wardrobe"`
	Persona      string `json:"persona"`
	VoiceProfile string `json:"voiceProfile"`
}

type UpdateSceneReq struct {
	Name      string   `json:"name" binding:"max=64"`
	Location  string   `json:"location"`
	TimeOfDay string   `json:"timeOfDay" binding:"max=32"`
	Lighting  string   `json:"lighting"`
	ColorTemp string   `json:"colorTemp" binding:"max=64"`
	Props     []string `json:"props"`
}

// ─────────── 日志 / 流水 / 订单 ───────────

type AuditLog struct {
	ID         string    `json:"id" db:"id"`
	UserID     string    `json:"userId" db:"user_id"`
	ProjectID  string    `json:"projectId" db:"project_id"`
	ShotID     string    `json:"shotId" db:"shot_id"`
	Action     string    `json:"action" db:"action"`
	FromStatus string    `json:"fromStatus" db:"from_status"`
	ToStatus   string    `json:"toStatus" db:"to_status"`
	Note       string    `json:"note" db:"note"`
	IP         string    `json:"ip" db:"ip"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
}

type CreditTransaction struct {
	ID           string    `json:"id" db:"id"`
	UserID       string    `json:"userId" db:"user_id"`
	Amount       int64     `json:"amount" db:"amount"`
	BalanceAfter int64     `json:"balanceAfter" db:"balance_after"`
	Kind         string    `json:"kind" db:"kind"`
	ProjectID    string    `json:"projectId" db:"project_id"`
	Note         string    `json:"note" db:"note"`
	OperatorID   string    `json:"operatorId" db:"operator_id"`
	CreatedAt    time.Time `json:"createdAt" db:"created_at"`
}

type RechargeOrder struct {
	ID                string     `json:"id" db:"id"`
	UserID            string     `json:"userId" db:"user_id"`
	AmountYuan        float64    `json:"amountYuan" db:"amount_yuan"`
	Credits           int64      `json:"credits" db:"credits"`
	Status            string     `json:"status" db:"status"`
	Channel           string     `json:"channel" db:"channel"`
	RunID             string     `json:"runId,omitempty" db:"run_id"`
	ExternalID        string     `json:"externalId" db:"external_id"`
	AdminNote         string     `json:"adminNote" db:"admin_note"`
	HandledBy         string     `json:"handledBy" db:"handled_by"`
	CreatedAt         time.Time  `json:"createdAt" db:"created_at"`
	HandledAt         time.Time  `json:"handledAt" db:"handled_at"`
	OutTradeNo        string     `json:"outTradeNo" db:"out_trade_no"`
	PayMode           string     `json:"payMode" db:"pay_mode"`
	CodeURL           string     `json:"codeUrl" db:"code_url"`
	H5URL             string     `json:"h5Url" db:"h5_url"`
	AmountCents       int64      `json:"amountCents" db:"amount_cents"`
	TransactionID     string     `json:"transactionId" db:"transaction_id"`
	Credited          bool       `json:"credited" db:"credited"`
	Mock              bool       `json:"mock" db:"mock"`
	PaidAt            time.Time  `json:"paidAt" db:"paid_at"`
	RefundAmountCents int64      `json:"refundAmountCents" db:"refund_amount_cents"`
	RefundReason      string     `json:"refundReason" db:"refund_reason"`
	RefundedAt        *time.Time `json:"refundedAt" db:"refunded_at"`
	RefundNo          string     `json:"refundNo" db:"refund_no"`
	User              *User      `json:"user,omitempty"`
}

type CreateOrderReq struct {
	AmountYuan float64 `json:"amountYuan" binding:"required,min=10,max=10000"`
	Channel    string  `json:"channel" binding:"max=32"`
	// RunID 是可选的续跑上下文；服务端会核验归属与可续跑状态，不信任客户端。
	RunID string `json:"runId" binding:"max=32"`
}

type HandleOrderReq struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note" binding:"max=255"`
}

type AdjustCreditsReq struct {
	UserID    string `json:"userId" binding:"required,max=64"`
	RequestID string `json:"requestId" binding:"required,uuid"`
	Amount    int64  `json:"amount" binding:"required"`
	Note      string `json:"note" binding:"max=255"`
}

// ─────────── 剧本（LLM 输出） ───────────

type ScriptOutput struct {
	Title       string        `json:"title"`
	Logline     string        `json:"logline"`
	StyleAnchor StyleAnchor   `json:"style_anchor"`
	Characters  []ScriptChar  `json:"characters"`
	Scenes      []ScriptScene `json:"scenes"`
	Shots       []ScriptShot  `json:"shots"`
}

type StyleAnchor struct {
	FilmReference    string `json:"film_reference"`
	ColorScheme      string `json:"color_scheme"`
	LightingStrategy string `json:"lighting_strategy"`
	Mood             string `json:"mood"`
}

type ScriptChar struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	AgeRange     string `json:"age_range"`
	Appearance   string `json:"appearance"`
	Wardrobe     string `json:"wardrobe"`
	Persona      string `json:"persona"`
	VoiceProfile string `json:"voice_profile"`
}

type ScriptScene struct {
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Location  string   `json:"location"`
	TimeOfDay string   `json:"time_of_day"`
	Lighting  string   `json:"lighting"`
	ColorTemp string   `json:"color_temp"`
	Props     []string `json:"props"`
}

type ScriptShot struct {
	ShotCode     string  `json:"shot_code"`
	SceneCode    string  `json:"scene_code"`
	Duration     float64 `json:"duration"`
	ShotSize     string  `json:"shot_size"`
	CameraHeight string  `json:"camera_height"`
	CameraMove   string  `json:"camera_move"`
	VisualPrompt string  `json:"visual_prompt"`
	SpeakerCode  string  `json:"speaker_code"`
	Dialogue     string  `json:"dialogue"`
	Soundscape   string  `json:"soundscape"`
	MusicCue     string  `json:"music_cue"`
}

// ─────────── 通用响应 ───────────

type APIResponse struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

type PageResp struct {
	Items    interface{} `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}
