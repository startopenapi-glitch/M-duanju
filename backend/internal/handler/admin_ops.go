package handler

import (
	"github.com/gin-gonic/gin"
)

// AdminRefundOrder 管理员退款
func (h *Handler) AdminRefundOrder(c *gin.Context) {
	var body struct {
		AmountCents int64  `json:"amountCents" binding:"required,gt=0"`
		Reason      string `json:"reason" binding:"required,max=80"`
	}
	if e := c.ShouldBindJSON(&body); e != nil {
		fail(c, 400, "请输入整数分退款金额和原因")
		return
	}
	v, e := h.svc.BeginRefund(c.Param("id"), c.GetString("userID"), body.AmountCents, body.Reason)
	adminResult(c, v, e)
}

// AdminRecompressKeyVisuals 批量压缩历史参考图（解决上游拉取超时）
func (h *Handler) AdminRecompressKeyVisuals(c *gin.Context) {
	n, err := h.svc.RecompressExistingKeyVisuals()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"recompressed": n, "message": "已转为 JPEG，size 降至上游可接受范围"})
}

// AdminJobs 任务队列状态（运维）
func (h *Handler) AdminJobs(c *gin.Context) {
	ok(c, gin.H{"counts": h.svc.CountJobs(c.Query("projectId"))})
}

// AdminReconcileJob 把运营在供货方后台核实过的任务 ID 绑定到「提交结果未知」的任务上。
// 不发起任何新提交，因此不会重复扣费。
func (h *Handler) AdminReconcileJob(c *gin.Context) {
	var in struct {
		Project        string `json:"project"`
		VerifiedTaskID string `json:"verifiedTaskId"`
		Reason         string `json:"reason" binding:"required,max=255"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Project == "" || in.VerifiedTaskID == "" {
		c.JSON(400, gin.H{"error": "需要 project 与 verifiedTaskId"})
		return
	}
	if err := h.svc.AdminReconcileSubmission(c.GetString("userID"), in.Project, c.Param("id"), in.VerifiedTaskID, in.Reason); err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, gin.H{"reconciled": true})
}

// AdminAbandonJob 放弃一个无法核对的任务并释放预算占用（不重新提交）。
func (h *Handler) AdminAbandonJob(c *gin.Context) {
	var in struct {
		Project string `json:"project"`
		Reason  string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&in)
	if in.Project == "" {
		c.JSON(400, gin.H{"error": "需要 project"})
		return
	}
	if err := h.svc.AbandonJob(in.Project, c.Param("id"), in.Reason); err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, gin.H{"abandoned": true})
}

// AdminOpenPartner 从已有账号开通合作方（不授予管理员权限）。
func (h *Handler) AdminOpenPartner(c *gin.Context) {
	var in struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
		Intro  string `json:"intro"`
		Wechat string `json:"wechat"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.UserID == "" {
		c.JSON(400, gin.H{"error": "需要 userId"})
		return
	}
	p, err := h.svc.PartnerStore().Create(c.Request.Context(), c.GetString("userID"), in.UserID, in.Name, in.Intro, in.Wechat)
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, p)
}

// AdminSetPartnerStatus 暂停/恢复，带版本校验。
func (h *Handler) AdminSetPartnerStatus(c *gin.Context) {
	var in struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}
	p, err := h.svc.PartnerStore().SetStatus(c.Request.Context(), c.GetString("userID"), c.Param("id"), in.Status, in.Version)
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, p)
}

// AdminUpdatePartner 修改公开资料，带版本校验。
func (h *Handler) AdminUpdatePartner(c *gin.Context) {
	var in struct {
		Name    string `json:"name"`
		Intro   string `json:"intro"`
		Wechat  string `json:"wechat"`
		Version int64  `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"error": "参数错误"})
		return
	}
	p, err := h.svc.PartnerStore().UpdateProfile(c.Request.Context(), c.GetString("userID"), c.Param("id"), in.Name, in.Intro, in.Wechat, in.Version)
	if err != nil {
		c.JSON(409, gin.H{"error": err.Error()})
		return
	}
	ok(c, p)
}
