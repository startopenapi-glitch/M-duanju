package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"drama-platform/internal/handler"
)

func Setup(h *handler.Handler, jwtSecret string, staticDir string) *gin.Engine {
	r := gin.Default()
	r.Use(cacheHeaders(staticDir))
	// Only the local reverse proxy may supply a forwarded client IP.
	r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"ok": true, "status": "healthy"})
		})

		// 认证
		api.POST("/auth/register", h.Register)
		api.POST("/auth/login", h.Login)
		api.POST("/auth/email-code", h.RequestEmailCode)
		api.POST("/auth/login-by-code", h.LoginByEmailCode)
		api.GET("/partner/public/:code", h.PartnerPublicProfile)
		api.GET("/partner/invitations/lookup", h.PartnerInvitationLookup)

		// 支付：公开端点
		api.GET("/pay/status", h.PayStatus)
		api.POST("/pay/notify", h.WechatNotify)

		// 需登录
		auth := api.Group("")
		auth.Use(h.AuthMiddleware(jwtSecret))

		// 代理自助：必须挂在鉴权组内，范围由会话决定，不接受客户端 partner_id
		auth.GET("/partner/me", h.PartnerMe)
		auth.GET("/partner/overview", h.PartnerOverview)
		auth.GET("/partner/customers", h.PartnerCustomers)
		auth.GET("/partner/orders", h.PartnerOrders)
		auth.PATCH("/partner/me", h.PartnerUpdateMe)
		auth.POST("/partner/invitations/accept", h.PartnerInvitationAccept)
		auth.POST("/projects/:id/runs", h.StartRun)
		{
			auth.GET("/auth/me", h.Me)
			auth.POST("/auth/password", h.ChangePassword)
			auth.GET("/auth/security", h.SecurityOverview)
			auth.GET("/auth/totp", h.TOTPStatus)
			auth.POST("/auth/totp/setup", h.TOTPSetup)
			auth.POST("/auth/totp/enable", h.TOTPEnable)
			auth.POST("/auth/totp/disable", h.TOTPDisable)

			// 项目列表 / 新建（无 :id，不能进归属校验组）
			auth.GET("/projects", h.ListProjects)
			auth.GET("/runs/context", h.UIRunContext)
			auth.GET("/orders/:id/progress", h.UIOrderProgress)
			auth.POST("/projects", h.CreateProject)
			auth.GET("/projects/estimate", h.EstimateProject)
			auth.GET("/projects/creation-context", h.CreationContext)

			// ── 单项目下所有操作：统一走归属校验中间件 ──
			pid := auth.Group("/projects/:id")
			pid.Use(h.OwnProject())
			{
				pid.GET("", h.GetProject)
				pid.PATCH("", h.UpdateProject)
				pid.DELETE("", h.DeleteProject)
				pid.GET("/runs/latest", h.GetLatestRun)
				pid.POST("/runs/:rid/cancel", h.CancelRun)

				// 剧本
				pid.POST("/script/generate", h.GenerateScript)
				pid.POST("/script/confirm", h.ConfirmScript)
				pid.PATCH("/shots/:sid", h.UpdateShot)
				pid.PATCH("/characters/:cid", h.UpdateCharacter)
				pid.PATCH("/scenes/:sid", h.UpdateScene)

				// 生成
				pid.POST("/keyvisual/queue", h.QueueKeyVisual)
				pid.POST("/keyvisual/confirm", h.ConfirmKeyVisual)
				pid.POST("/keyvisual/approve", h.ApproveKeyVisual)
				pid.POST("/shots/queue", h.QueueShots)
				pid.POST("/shots/poll", h.PollShots)
				pid.POST("/jobs/:jid/resume", h.ResumeGenerationJob)
				pid.POST("/shots/:seq/approve", h.ApproveShot)
				pid.POST("/shots/:seq/unapprove", h.UnapproveShot)
				pid.POST("/shots/:seq/regenerate", h.RegenerateShot)
				pid.POST("/assemble", h.Assemble)
			}

			// 积分
			auth.GET("/credits", h.GetBalance)
			auth.POST("/orders", h.CreateOrder)
			auth.GET("/orders", h.ListMyOrders)

			// 支付
			auth.POST("/pay/create", h.CreatePayOrder)
			auth.GET("/pay/status/:id", h.GetPayStatus)
			auth.POST("/pay/orders/:id/confirm", h.ConfirmOrder)

			// 管理
			admin := auth.Group("/admin")
			admin.Use(handler.AdminMiddleware())
			{
				admin.GET("/stats", h.AdminStats)
				admin.GET("/overview", h.AdminOverview)
				admin.GET("/orders/:id/detail", h.AdminOrderDetail)
				admin.POST("/orders/:id/refund/query", h.AdminQueryRefund)
				admin.POST("/orders/:id/confirm", h.AdminConfirmOrder)
				admin.GET("/jobs/list", h.AdminJobsList)
				admin.GET("/jobs/:id/detail", h.AdminJobDetail)
				admin.GET("/users/:id/detail", h.AdminUserDetail)
				admin.POST("/users/:id/status", h.AdminUserStatus)
				admin.GET("/credits", h.AdminCredits)
				admin.GET("/audit", h.AdminAudit)
				admin.GET("/system", h.AdminSystem)
				admin.GET("/users", h.AdminListUsers)
				admin.POST("/users/credits", h.AdminAdjustCredits)
				admin.GET("/orders", h.AdminListOrders)
				admin.POST("/orders/:id/handle", h.AdminHandleOrder)
				admin.POST("/orders/:id/refund", h.AdminRefundOrder)
				admin.GET("/jobs", h.AdminJobs)
				admin.GET("/pay/diagnose", h.AdminPayDiagnose)
				admin.POST("/keyvisuals/recompress", h.AdminRecompressKeyVisuals)
				admin.POST("/jobs/:id/reconcile", h.AdminReconcileJob)
				admin.POST("/jobs/:id/abandon", h.AdminAbandonJob)
				admin.POST("/partners", h.AdminOpenPartner)
				admin.POST("/partners/:id", h.AdminUpdatePartner)
				admin.POST("/partners/:id/status", h.AdminSetPartnerStatus)
				admin.GET("/partners", h.AdminListPartners)
				admin.POST("/partner-invitations", h.AdminCreatePartnerInvitation)
				admin.GET("/partner-invitations", h.AdminListPartnerInvitations)
				admin.POST("/partner-invitations/:id/resend", h.AdminResendPartnerInvitation)
				admin.POST("/partner-invitations/:id/revoke", h.AdminRevokePartnerInvitation)
			}
		}
	}

	if staticDir != "" {
		r.Static("/assets", staticDir+"/assets")
	}
	r.NoRoute(func(c *gin.Context) { servePublic(c, staticDir) })
	return r
}
