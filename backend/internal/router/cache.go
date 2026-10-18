package router

import (
	"github.com/gin-gonic/gin"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var fingerprint = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.(js|css|woff2?|png|webp|svg)$`)

func cacheHeaders(dir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		c.Header("Cache-Control", "no-cache")
		if path == "/api" || strings.HasPrefix(path, "/api/") {
			c.Header("Cache-Control", "no-store")
		} else if dir != "" && strings.HasPrefix(path, "/assets/") && fingerprint.MatchString(filepath.Base(path)) {
			if _, ok := publicFile(dir, path); ok {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
		}
		c.Next()
	}
}
func publicFile(dir, path string) (string, bool) {
	if dir == "" {
		return "", false
	}
	root, e := filepath.Abs(dir)
	if e != nil {
		return "", false
	}
	file := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "/")))
	rel, e := filepath.Rel(root, file)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	st, e := os.Stat(file)
	return file, e == nil && st.Mode().IsRegular()
}
func servePublic(c *gin.Context, dir string) {
	path := c.Request.URL.Path
	if dir == "" || c.Request.Method != "GET" && c.Request.Method != "HEAD" || path == "/api" || strings.HasPrefix(path, "/api/") || path == "/assets" || strings.HasPrefix(path, "/assets/") {
		c.JSON(404, gin.H{"ok": false, "error": "not found"})
		return
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" && ext != ".html" {
		allowed := map[string]bool{".ico": true, ".svg": true, ".png": true, ".jpg": true, ".webp": true, ".webmanifest": true, ".txt": true}
		if allowed[ext] {
			if file, ok := publicFile(dir, path); ok {
				c.File(file)
				return
			}
		}
		c.Status(404)
		return
	}
	c.File(filepath.Join(dir, "index.html"))
}
