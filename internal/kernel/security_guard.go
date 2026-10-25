package kernel

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/security"
)

// initSecurity 加载应用锁设置并准备会话表。
func (k *Kernel) initSecurity() error {
	store, err := security.Open(k.dataDir)
	if err != nil {
		return fmt.Errorf("加载安全设置失败: %w", err)
	}
	k.sec = store
	k.sessions = security.NewSessions(24 * time.Hour)
	k.throttle = &security.Throttle{}
	k.wallpaperDir = filepath.Join(k.dataDir, "wallpapers")
	return nil
}

// securityGuard 在已设置应用密码时拦截未解锁的请求。
func (k *Kernel) securityGuard(c *fiber.Ctx) error {
	if k.sec == nil || !k.sec.HasPassword() {
		return c.Next()
	}
	path := c.Path()
	if !needsUnlock(path) || isPublicSecurityPath(path) {
		return c.Next()
	}
	if k.sessions.Valid(c.Cookies(security.SessionCookie)) {
		return c.Next()
	}
	if strings.HasPrefix(path, "/api") {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "DevOS 已锁定，请先输入应用密码解锁",
			"locked": true,
		})
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store")
	return c.Status(fiber.StatusUnauthorized).SendString(lockedPage)
}

const lockedPage = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>DevOS 已锁定</title></head>
<body style="margin:0;display:flex;height:100vh;align-items:center;justify-content:center;background:#f8fafc;font-family:system-ui,sans-serif;color:#334155">
<div style="text-align:center"><div style="font-size:15px;font-weight:600;color:#0f172a">DevOS 已锁定</div>
<p style="margin-top:8px;font-size:12px;color:#64748b">请回到桌面输入应用密码后再访问此页面。</p></div>
</body></html>`

// needsUnlock 判断请求是否属于需要解锁后才能访问的资源。
func needsUnlock(path string) bool {
	switch {
	case path == "/api", strings.HasPrefix(path, "/api/"):
		return true
	case path == "/apps", strings.HasPrefix(path, "/apps/"):
		return true
	case path == "/mock", strings.HasPrefix(path, "/mock/"):
		return true
	default:
		return false
	}
}

// isPublicSecurityPath 是锁定状态下仍需放行的接口（锁屏与壁纸展示）。
func isPublicSecurityPath(path string) bool {
	switch path {
	case "/api/health", "/api/security/status", "/api/security/unlock", "/api/security/locale":
		return true
	}
	return strings.HasPrefix(path, "/api/security/wallpaper/file/")
}

// verifyRequestOrigin 校验安全类写操作来自同源的 DevOS 桌面，降低 CSRF 风险。
func (k *Kernel) verifyRequestOrigin(c *fiber.Ctx) error {
	if c.Get("X-DevOS-Request") != "1" {
		return fiber.NewError(fiber.StatusBadRequest, "缺少请求标识，请从 DevOS 桌面操作")
	}
	origin := strings.TrimSpace(c.Get("Origin"))
	if origin == "" {
		return nil
	}
	base, err := url.Parse(k.baseURL)
	if err != nil {
		return fiber.NewError(fiber.StatusForbidden, "请求来源不受信任")
	}
	parsed, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
		return fiber.NewError(fiber.StatusForbidden, "请求来源不受信任")
	}
	return nil
}

// issueSession 为当前浏览器签发解锁 Cookie。
func (k *Kernel) issueSession(c *fiber.Ctx) {
	token := k.sessions.Issue()
	c.Cookie(&fiber.Cookie{
		Name:     security.SessionCookie,
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Strict",
	})
}

// clearSession 撤销并清除当前解锁 Cookie。
func (k *Kernel) clearSession(c *fiber.Ctx) {
	k.sessions.Revoke(c.Cookies(security.SessionCookie))
	c.Cookie(&fiber.Cookie{
		Name:     security.SessionCookie,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Strict",
		Expires:  time.Now().Add(-time.Hour),
	})
}

// statusPayload 返回可公开的安全状态，绝不包含密码哈希。
func (k *Kernel) statusPayload(c *fiber.Ctx) fiber.Map {
	snapshot := k.sec.Snapshot()
	hasPassword := snapshot.Password != nil
	locked := hasPassword && !k.sessions.Valid(c.Cookies(security.SessionCookie))
	return fiber.Map{
		"hasPassword": hasPassword,
		"locked":      locked,
		"locale":      snapshot.Locale,
		"wallpaper":   snapshot.Wallpaper,
	}
}
