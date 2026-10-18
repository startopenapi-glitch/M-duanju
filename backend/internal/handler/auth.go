package handler

import (
	"github.com/gin-gonic/gin"
)

// AuthMiddleware 暴露给 router 使用
func (h *Handler) AuthMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if authorization == "" || len(authorization) < 8 || authorization[:7] != "Bearer " {
			c.AbortWithStatusJSON(401, gin.H{"ok": false, "error": "未登录"})
			return
		}
		userID, version, err := parseToken(authorization[7:], secret)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"ok": false, "error": "登录已过期，请重新登录"})
			return
		}
		role, err := h.svc.CurrentSession(userID, version)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"ok": false, "error": "登录已过期，请重新登录"})
			return
		}
		c.Set("userID", userID)
		c.Set("role", role)
		c.Next()
	}
}

// AdminMiddleware 管理员校验
func AdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") != "admin" {
			c.AbortWithStatusJSON(403, gin.H{"ok": false, "error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}
