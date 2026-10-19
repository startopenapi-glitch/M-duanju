package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/middleware"
	"drama-platform/internal/model"
	"drama-platform/internal/service"
)

// 占位（避免未使用导入）
var _ = time.Now
var _ = strconv.Itoa
var _ = strings.Contains

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

func itoa(i int) string { return strconv.Itoa(i) }

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type genContext = gin.Context

// 时间辅助
func nowPlus(sec int64) time.Time {
	return time.Now().Add(time.Duration(sec) * time.Second)
}

var _ = http.StatusOK
var _ = model.User{}
var _ = service.New
var _ = middleware.JWTAuth
