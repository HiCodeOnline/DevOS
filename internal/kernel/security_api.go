package kernel

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码器用于校验上传壁纸
	_ "image/png"  // 注册 PNG 解码器用于校验上传壁纸
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/security"
)

const (
	maxPasswordAttemptsBackoff = 30 * time.Second
	maxWallpaperBytes          = 8 << 20
)

var wallpaperFileRe = regexp.MustCompile(`^wallpaper-[0-9]+-[0-9a-f]{8}\.(png|jpe?g)$`)

func (k *Kernel) registerSecurityRoutes(app *fiber.App) {
	api := app.Group("/api/security")
	api.Get("/status", k.handleSecurityStatus)
	api.Post("/unlock", k.handleSecurityUnlock)
	api.Post("/lock", k.handleSecurityLock)
	api.Post("/password", k.handleSecurityPassword)
	api.Post("/settings", k.handleSecuritySettings)
	api.Post("/locale", k.handleSecurityLocale)
	api.Post("/wallpaper", k.handleWallpaperUpload)
	api.Get("/wallpaper/file/:name", k.handleWallpaperFile)
}

func (k *Kernel) handleSecurityStatus(c *fiber.Ctx) error {
	// 状态决定是否显示锁屏，禁止浏览器缓存以免用到过期的锁定状态。
	c.Set("Cache-Control", "no-store")
	return c.JSON(k.statusPayload(c))
}

func (k *Kernel) handleSecurityUnlock(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	if wait := k.throttle.Blocked(); wait > 0 {
		seconds := int(wait.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		c.Set("Retry-After", strconv.Itoa(seconds))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":      "密码错误次数过多，请稍后再试",
			"retryAfter": seconds,
		})
	}
	if !k.sec.HasPassword() {
		k.issueSession(c)
		return c.JSON(fiber.Map{"ok": true, "locked": false})
	}
	if err := k.sec.Verify(in.Password); err != nil {
		k.throttle.Failure()
		if errors.Is(err, security.ErrWrongPassword) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "密码不正确"})
		}
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	k.throttle.Success()
	k.issueSession(c)
	return c.JSON(fiber.Map{"ok": true, "locked": false})
}

func (k *Kernel) handleSecurityLock(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	k.clearSession(c)
	return c.JSON(fiber.Map{"ok": true, "locked": k.sec.HasPassword()})
}

func (k *Kernel) handleSecurityPassword(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	var in struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}

	hadPassword := k.sec.HasPassword()
	var err error
	switch {
	case !hadPassword:
		if strings.TrimSpace(in.NewPassword) == "" {
			return fiber.NewError(fiber.StatusBadRequest, "请输入新密码")
		}
		err = k.sec.SetPassword(in.NewPassword)
	case strings.TrimSpace(in.NewPassword) == "":
		err = k.sec.RemovePassword(in.OldPassword)
	default:
		err = k.sec.ChangePassword(in.OldPassword, in.NewPassword)
	}
	if err != nil {
		switch {
		case errors.Is(err, security.ErrWrongPassword):
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		case errors.Is(err, security.ErrWeakPassword), errors.Is(err, security.ErrNoPassword):
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		default:
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
	}

	// 密码发生变化：旧会话全部失效，并给当前浏览器重新签发。
	k.sessions.RevokeAll()
	if k.sec.HasPassword() {
		k.issueSession(c)
	} else {
		k.clearSession(c)
	}
	return c.JSON(fiber.Map{"ok": true, "hasPassword": k.sec.HasPassword()})
}

func (k *Kernel) handleSecuritySettings(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	var in struct {
		Locale    string              `json:"locale"`
		Wallpaper *security.Wallpaper `json:"wallpaper"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	if strings.TrimSpace(in.Locale) != "" {
		if err := k.sec.SetLocale(in.Locale); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
	}
	if in.Wallpaper != nil {
		wallpaper := *in.Wallpaper
		if wallpaper.Kind == security.WallpaperCustom {
			return fiber.NewError(fiber.StatusBadRequest, "自定义壁纸请通过上传接口设置")
		}
		if err := k.sec.SetWallpaper(wallpaper); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
	}
	return c.JSON(k.statusPayload(c))
}

// handleSecurityLocale 允许锁屏界面在解锁前切换语言并持久化。
func (k *Kernel) handleSecurityLocale(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	var in struct {
		Locale string `json:"locale"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	if err := k.sec.SetLocale(in.Locale); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true, "locale": k.sec.Locale()})
}

func (k *Kernel) handleWallpaperUpload(c *fiber.Ctx) error {
	if err := k.verifyRequestOrigin(c); err != nil {
		return err
	}
	file, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "请选择要上传的图片")
	}
	if file.Size <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "图片内容为空")
	}
	if file.Size > maxWallpaperBytes {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "图片大小不能超过 8MB")
	}
	src, err := file.Open()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "读取图片失败")
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, maxWallpaperBytes+1))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "读取图片失败")
	}
	if len(data) > maxWallpaperBytes {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "图片大小不能超过 8MB")
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "仅支持 PNG 或 JPEG 格式的图片")
	} else if format != "png" && format != "jpeg" {
		return fiber.NewError(fiber.StatusBadRequest, "仅支持 PNG 或 JPEG 格式的图片")
	}
	ext := "png"
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8}) {
		ext = "jpg"
	}

	if err := os.MkdirAll(k.wallpaperDir, 0o700); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "无法创建壁纸目录")
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	name := fmt.Sprintf("wallpaper-%d-%s.%s", time.Now().UnixNano(), hex.EncodeToString(buf), ext)
	if err := os.WriteFile(filepath.Join(k.wallpaperDir, name), data, 0o600); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "保存壁纸失败")
	}

	previous := k.sec.Wallpaper()
	if previous.Kind == security.WallpaperCustom && previous.File != "" && previous.File != name {
		_ = os.Remove(filepath.Join(k.wallpaperDir, filepath.Base(previous.File)))
	}
	wallpaper := security.Wallpaper{Kind: security.WallpaperCustom, File: name}
	if err := k.sec.SetWallpaper(wallpaper); err != nil {
		_ = os.Remove(filepath.Join(k.wallpaperDir, name))
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{
		"ok":        true,
		"wallpaper": wallpaper,
		"url":       "/api/security/wallpaper/file/" + name,
	})
}

func (k *Kernel) handleWallpaperFile(c *fiber.Ctx) error {
	name := c.Params("name")
	if !wallpaperFileRe.MatchString(name) || filepath.Base(name) != name {
		return fiber.NewError(fiber.StatusNotFound, "壁纸不存在")
	}
	data, err := os.ReadFile(filepath.Join(k.wallpaperDir, name))
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "壁纸不存在")
	}
	contentType := "image/png"
	if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") {
		contentType = "image/jpeg"
	}
	c.Set("Content-Type", contentType)
	c.Set("Cache-Control", "no-cache")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.Send(data)
}
