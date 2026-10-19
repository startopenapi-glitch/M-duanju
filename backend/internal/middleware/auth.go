package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func JWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if h == "" || !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "未登录"})
			return
		}
		tokenStr := strings.TrimPrefix(h, "Bearer ")
		t, err := jwt.Parse(tokenStr, func(tk *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		if err != nil || !t.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "登录已过期，请重新登录"})
			return
		}
		claims, ok := t.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "无效的登录凭证"})
			return
		}
		sub, _ := claims["sub"].(string)
		role, _ := claims["role"].(string)
		if sub == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "无效的登录凭证"})
			return
		}
		c.Set("userID", sub)
		c.Set("role", role)
		c.Next()
	}
}

func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"ok": false, "error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}
