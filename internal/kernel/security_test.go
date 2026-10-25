package kernel

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v2"
)

func newSecurityKernel(t *testing.T) *Kernel {
	t.Helper()
	web := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("desktop")},
		"assets/app.js": &fstest.MapFile{Data: []byte("javascript")},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	k := New(web, t.TempDir(), "http://127.0.0.1:7788", logger)
	if err := k.initSecurity(); err != nil {
		t.Fatalf("initSecurity: %v", err)
	}
	// 使用全新的 Fiber 实例，避免与生产路由配置耦合。
	k.app = fiber.New(fiber.Config{ErrorHandler: errorHandler})
	k.routes()
	return k
}

type apiResponse struct {
	Code    int
	Body    []byte
	Cookies []*http.Cookie
}

func request(t *testing.T, k *Kernel, method, path, body string, cookies []*http.Cookie, headers map[string]string) apiResponse {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(cookies) > 0 {
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res, err := k.app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return apiResponse{Code: res.StatusCode, Body: data, Cookies: res.Cookies()}
}

func devosHeaders(extra map[string]string) map[string]string {
	out := map[string]string{"X-DevOS-Request": "1"}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func setPassword(t *testing.T, k *Kernel, password string) []*http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"newPassword": password})
	res := request(t, k, "POST", "/api/security/password", string(body), nil, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("设置密码失败: %d %s", res.Code, res.Body)
	}
	return res.Cookies
}

func TestGuardAllowsEverythingWithoutPassword(t *testing.T) {
	k := newSecurityKernel(t)
	for _, path := range []string{"/api/apps", "/api/system", "/api/apps/db/connections", "/apps/db/index.html", "/mock/hello", "/assets/app.js"} {
		if res := request(t, k, "GET", path, "", nil, nil); res.Code == fiber.StatusUnauthorized {
			t.Fatalf("无密码时 %s 不应被拦截: %s", path, res.Body)
		}
	}
	res := request(t, k, "GET", "/api/security/status", "", nil, nil)
	if res.Code != fiber.StatusOK {
		t.Fatalf("status 状态码 = %d", res.Code)
	}
	var status struct {
		HasPassword bool `json:"hasPassword"`
		Locked      bool `json:"locked"`
		Locale      string
		Wallpaper   struct {
			Kind   string `json:"kind"`
			Preset string `json:"preset"`
		} `json:"wallpaper"`
	}
	if err := json.Unmarshal(res.Body, &status); err != nil {
		t.Fatalf("解析 status: %v (%s)", err, res.Body)
	}
	if status.HasPassword || status.Locked {
		t.Fatalf("初始状态不应锁定: %s", res.Body)
	}
	if status.Locale != "" || status.Wallpaper.Kind != "preset" {
		t.Fatalf("初始设置不符（语言应跟随系统）: %s", res.Body)
	}
	if strings.Contains(string(res.Body), "hash") || strings.Contains(string(res.Body), "salt") {
		t.Fatalf("status 不应泄露密码哈希: %s", res.Body)
	}
}

func TestGuardBlocksWhileLocked(t *testing.T) {
	k := newSecurityKernel(t)
	setPassword(t, k, "lock-pass-1")

	for _, path := range []string{"/api/apps", "/api/system", "/api/apps/db/connections", "/apps/db/index.html", "/mock/hello"} {
		res := request(t, k, "GET", path, "", nil, nil)
		if res.Code != fiber.StatusUnauthorized {
			t.Fatalf("锁定状态下 %s 应返回 401，实际 %d", path, res.Code)
		}
	}
	for _, path := range []string{"/api/health", "/api/security/status", "/assets/app.js", "/"} {
		res := request(t, k, "GET", path, "", nil, nil)
		if res.Code != fiber.StatusOK {
			t.Fatalf("锁定状态下 %s 应放行，实际 %d", path, res.Code)
		}
	}

	res := request(t, k, "GET", "/apps/db/index.html", "", nil, nil)
	if !strings.Contains(string(res.Body), "DevOS 已锁定") {
		t.Fatalf("应用页面应返回锁定提示: %s", res.Body)
	}
}

func TestUnlockFlowAndWrongPasswordThrottle(t *testing.T) {
	k := newSecurityKernel(t)
	setPassword(t, k, "unlock-pass")

	body, _ := json.Marshal(map[string]string{"password": "unlock-pass"})
	// 缺少 X-DevOS-Request 应拒绝。
	if res := request(t, k, "POST", "/api/security/unlock", string(body), nil, nil); res.Code != fiber.StatusBadRequest {
		t.Fatalf("缺少请求标识应返回 400，实际 %d", res.Code)
	}
	// 伪造 Origin 应拒绝。
	if res := request(t, k, "POST", "/api/security/unlock", string(body), nil, map[string]string{
		"X-DevOS-Request": "1",
		"Origin":          "http://evil.example",
	}); res.Code != fiber.StatusForbidden {
		t.Fatalf("跨站 Origin 应返回 403，实际 %d", res.Code)
	}

	wrong, _ := json.Marshal(map[string]string{"password": "not-the-pass"})
	if res := request(t, k, "POST", "/api/security/unlock", string(wrong), nil, devosHeaders(nil)); res.Code != fiber.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401，实际 %d", res.Code)
	}

	for i := 0; i < 5; i++ {
		request(t, k, "POST", "/api/security/unlock", string(wrong), nil, devosHeaders(nil))
	}
	if res := request(t, k, "POST", "/api/security/unlock", string(wrong), nil, devosHeaders(nil)); res.Code != fiber.StatusTooManyRequests {
		t.Fatalf("连续失败后应返回 429，实际 %d %s", res.Code, res.Body)
	}

	// 退避期间即使密码正确也应被限流。
	if res := request(t, k, "POST", "/api/security/unlock", string(body), nil, devosHeaders(nil)); res.Code != fiber.StatusTooManyRequests {
		t.Fatalf("退避期间应保持 429，实际 %d", res.Code)
	}
}

func TestSessionCookieGrantsAccessAndLockRevokes(t *testing.T) {
	k := newSecurityKernel(t)
	cookies := setPassword(t, k, "session-pass")

	if res := request(t, k, "GET", "/api/apps", "", nil, nil); res.Code != fiber.StatusUnauthorized {
		t.Fatalf("无 Cookie 应被拦截: %d", res.Code)
	}
	if res := request(t, k, "GET", "/api/apps", "", cookies, nil); res.Code != fiber.StatusOK {
		t.Fatalf("带 Cookie 应放行: %d %s", res.Code, res.Body)
	}

	if res := request(t, k, "POST", "/api/security/lock", "", cookies, devosHeaders(nil)); res.Code != fiber.StatusOK {
		t.Fatalf("锁定失败: %d %s", res.Code, res.Body)
	}
	if res := request(t, k, "GET", "/api/apps", "", cookies, nil); res.Code != fiber.StatusUnauthorized {
		t.Fatalf("锁定后旧 Cookie 应失效: %d", res.Code)
	}

	body, _ := json.Marshal(map[string]string{"password": "session-pass"})
	res := request(t, k, "POST", "/api/security/unlock", string(body), nil, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("解锁失败: %d %s", res.Code, res.Body)
	}
	if res := request(t, k, "GET", "/api/apps", "", res.Cookies, nil); res.Code != fiber.StatusOK {
		t.Fatalf("新 Cookie 应放行: %d", res.Code)
	}
}

func TestChangePasswordRequiresOldAndInvalidatesSessions(t *testing.T) {
	k := newSecurityKernel(t)
	cookies := setPassword(t, k, "old-pass-1")

	wrong, _ := json.Marshal(map[string]string{"oldPassword": "bad", "newPassword": "new-pass-2"})
	if res := request(t, k, "POST", "/api/security/password", string(wrong), cookies, devosHeaders(nil)); res.Code != fiber.StatusUnauthorized {
		t.Fatalf("旧密码错误应返回 401，实际 %d %s", res.Code, res.Body)
	}

	short, _ := json.Marshal(map[string]string{"oldPassword": "old-pass-1", "newPassword": "123"})
	if res := request(t, k, "POST", "/api/security/password", string(short), cookies, devosHeaders(nil)); res.Code != fiber.StatusBadRequest {
		t.Fatalf("新密码过短应返回 400，实际 %d", res.Code)
	}

	good, _ := json.Marshal(map[string]string{"oldPassword": "old-pass-1", "newPassword": "new-pass-2"})
	res := request(t, k, "POST", "/api/security/password", string(good), cookies, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("修改密码失败: %d %s", res.Code, res.Body)
	}
	if res := request(t, k, "GET", "/api/apps", "", cookies, nil); res.Code != fiber.StatusUnauthorized {
		t.Fatalf("修改密码后旧会话应失效: %d", res.Code)
	}
	if res := request(t, k, "GET", "/api/apps", "", res.Cookies, nil); res.Code != fiber.StatusOK {
		t.Fatalf("修改密码后新会话应有效: %d", res.Code)
	}
}

func TestRemovePasswordUnlocksEverything(t *testing.T) {
	k := newSecurityKernel(t)
	cookies := setPassword(t, k, "remove-pass")

	wrong, _ := json.Marshal(map[string]string{"oldPassword": "bad", "newPassword": ""})
	if res := request(t, k, "POST", "/api/security/password", string(wrong), cookies, devosHeaders(nil)); res.Code != fiber.StatusUnauthorized {
		t.Fatalf("移除密码时旧密码错误应返回 401，实际 %d", res.Code)
	}

	body, _ := json.Marshal(map[string]string{"oldPassword": "remove-pass", "newPassword": ""})
	res := request(t, k, "POST", "/api/security/password", string(body), cookies, devosHeaders(nil))
	if res.Code != fiber.StatusOK {
		t.Fatalf("移除密码失败: %d %s", res.Code, res.Body)
	}
	if res := request(t, k, "GET", "/api/apps", "", nil, nil); res.Code != fiber.StatusOK {
		t.Fatalf("移除密码后应直接放行: %d", res.Code)
	}
}

func TestLocaleChangeRequiresUnlock(t *testing.T) {
	k := newSecurityKernel(t)
	setPassword(t, k, "locale-pass")

	// 锁屏界面不再提供语言切换，语言变更只能在解锁后经设置接口完成。
	body, _ := json.Marshal(map[string]string{"locale": "zh-TW"})
	res := request(t, k, "POST", "/api/security/settings", string(body), nil, devosHeaders(nil))
	if res.Code != fiber.StatusUnauthorized {
		t.Fatalf("锁定状态下不应允许修改语言: %d %s", res.Code, res.Body)
	}

	var status struct {
		Locale string `json:"locale"`
	}
	_ = json.Unmarshal(request(t, k, "GET", "/api/security/status", "", nil, nil).Body, &status)
	if status.Locale != "" {
		t.Fatalf("未解锁时语言应保持跟随系统: %s", status.Locale)
	}
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	// 1x1 透明 PNG。
	data := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	return data
}

func uploadWallpaper(t *testing.T, k *Kernel, cookies []*http.Cookie, filename string, data []byte) apiResponse {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造表单: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("写入表单: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/api/security/wallpaper", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-DevOS-Request", "1")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	res, err := k.app.Test(req, -1)
	if err != nil {
		t.Fatalf("上传壁纸: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return apiResponse{Code: res.StatusCode, Body: body}
}

func TestWallpaperUploadAndServing(t *testing.T) {
	k := newSecurityKernel(t)
	cookies := setPassword(t, k, "wallpaper-pass")

	if res := uploadWallpaper(t, k, cookies, "bg.png", []byte("not an image")); res.Code != fiber.StatusBadRequest {
		t.Fatalf("伪装成 PNG 的文本应被拒绝: %d", res.Code)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	if res := uploadWallpaper(t, k, cookies, "bg.svg", svg); res.Code != fiber.StatusBadRequest {
		t.Fatalf("SVG 应被拒绝: %d", res.Code)
	}

	res := uploadWallpaper(t, k, cookies, "bg.png", pngFixture(t))
	if res.Code != fiber.StatusOK {
		t.Fatalf("合法 PNG 上传失败: %d %s", res.Code, res.Body)
	}
	var out struct {
		URL       string `json:"url"`
		Wallpaper struct {
			Kind string `json:"kind"`
			File string `json:"file"`
		} `json:"wallpaper"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatalf("解析上传结果: %v", err)
	}
	if out.Wallpaper.Kind != "custom" || out.Wallpaper.File == "" || out.URL == "" {
		t.Fatalf("上传结果不完整: %s", res.Body)
	}

	// 第二次上传应清理旧的壁纸文件。
	second := uploadWallpaper(t, k, cookies, "bg2.png", pngFixture(t))
	if second.Code != fiber.StatusOK {
		t.Fatalf("第二次上传失败: %d", second.Code)
	}
	var out2 struct {
		URL       string `json:"url"`
		Wallpaper struct {
			File string `json:"file"`
		} `json:"wallpaper"`
	}
	_ = json.Unmarshal(second.Body, &out2)
	if out2.Wallpaper.File == out.Wallpaper.File {
		t.Fatal("第二次上传应生成新文件名")
	}
	old := request(t, k, "GET", out.URL, "", nil, nil)
	if old.Code != fiber.StatusNotFound {
		t.Fatalf("旧壁纸文件应被删除，实际 %d", old.Code)
	}

	served := request(t, k, "GET", out2.URL, "", nil, nil)
	if served.Code != fiber.StatusOK || !bytes.Equal(served.Body, pngFixture(t)) {
		t.Fatalf("壁纸读取失败: %d", served.Code)
	}

	for _, bad := range []string{"/api/security/wallpaper/file/evil.png", "/api/security/wallpaper/file/..%2F..%2Fsecurity.json"} {
		if res := request(t, k, "GET", bad, "", nil, nil); res.Code != fiber.StatusNotFound {
			t.Fatalf("非法壁纸路径 %s 应返回 404，实际 %d", bad, res.Code)
		}
	}
}
