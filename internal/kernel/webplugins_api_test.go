package kernel

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

const nextIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#0ea5e9"/></svg>`

// stubPlugin 是测试用内置插件，用于验证内置静态资源路由仍可用。
type stubPlugin struct {
	manifest plugin.Manifest
	assets   fstest.MapFS
}

func (s *stubPlugin) Manifest() plugin.Manifest      { return s.manifest }
func (s *stubPlugin) Init(ctx *plugin.Context) error { return nil }
func (s *stubPlugin) Register(api fiber.Router)      {}
func (s *stubPlugin) Assets() fs.FS                  { return s.assets }

func newTestKernel(t *testing.T) *Kernel {
	t.Helper()
	dataDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	k := New(fstest.MapFS{"index.html": {Data: []byte("desktop")}}, dataDir, "http://127.0.0.1:0", log)
	stub := &stubPlugin{
		manifest: plugin.Manifest{ID: "stub", Name: "Stub", Entry: "index.html"},
		assets:   fstest.MapFS{"index.html": {Data: []byte("builtin-home")}},
	}
	if err := k.Load([]plugin.Plugin{stub}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return k
}

func doJSON(t *testing.T, k *Kernel, method, target string, body io.Reader, contentType string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := k.app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, raw
}

func getText(t *testing.T, k *Kernel, target string) (int, string) {
	t.Helper()
	resp, raw := doJSON(t, k, http.MethodGet, target, nil, "")
	return resp.StatusCode, string(raw)
}

// installZip 以 multipart 方式上传 zip。
func installZip(t *testing.T, k *Kernel, zipPath string, fields map[string]string) (int, map[string]any) {
	t.Helper()
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatalf("读取 %s: %v", zipPath, err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(zipPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		_ = mw.WriteField(key, value)
	}
	_ = mw.Close()

	resp, raw := doJSON(t, k, http.MethodPost, "/api/plugins/install", &buf, mw.FormDataContentType())
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return resp.StatusCode, out
}

// makeZip 用给定条目生成 zip；条目名支持 ../ 前缀以便测试 Zip Slip。
func makeZip(t *testing.T, zipPath string, entries map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(strings.ReplaceAll(name, "\\", "/"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

// writeTree 在 base 下按相对路径写入文件。
func writeTree(t *testing.T, base string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func nextPluginJSON(version string) string {
	return fmt.Sprintf(`{"id":"nextdemo","name":"Next Demo","description":"Next.js static export sample","version":"%s","category":"Sample","icon":"icon.svg","accent":"#0ea5e9","entry":"index.html","staticRoot":"out","spa":true,"specVersion":1}`, version)
}

func nextDemoFiles(version string) map[string]string {
	return map[string]string{
		"plugin.json":                    nextPluginJSON(version),
		"out/index.html":                 "<!doctype html><html><body><h1>NEXT HOME v" + version + "</h1></body></html>",
		"out/about.html":                 "<!doctype html><html><body><h1>ABOUT HTML</h1></body></html>",
		"out/icon.svg":                   nextIconSVG,
		"out/_next/static/chunks/app.js": `window.__NEXT_DEMO__ = "` + version + `";`,
	}
}

// nextDemoZip 生成单顶层目录布局的 Next.js 插件包。
func nextDemoZip(t *testing.T, version string) string {
	t.Helper()
	entries := map[string]string{}
	for name, content := range nextDemoFiles(version) {
		entries["nextdemo/"+name] = content
	}
	return makeZip(t, filepath.Join(t.TempDir(), "nextdemo-"+version+".zip"), entries)
}

// invalidZip 生成各类不合规插件包。
func invalidZip(t *testing.T, kind string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), kind+".zip")
	switch kind {
	case "badicon":
		return makeZip(t, path, map[string]string{
			"plugin.json":    `{"id":"badicon","name":"Bad Icon","version":"1.0.0","icon":"missing.png","entry":"index.html","specVersion":1}`,
			"web/index.html": "<h1>bad icon</h1>",
		})
	case "badspec":
		return makeZip(t, path, map[string]string{
			"plugin.json":    `{"id":"badspec","name":"Bad Spec","version":"1.0.0","entry":"index.html","specVersion":2}`,
			"web/index.html": "<h1>bad spec</h1>",
		})
	case "nomanifest":
		return makeZip(t, path, map[string]string{
			"web/index.html": "<h1>no manifest</h1>",
		})
	case "slip":
		return makeZip(t, path, map[string]string{
			"plugin.json": `{"id":"slip","name":"Slip","version":"1.0.0","entry":"index.html"}`,
			"../evil.txt": "pwned",
		})
	}
	t.Fatalf("未知夹具类型: %s", kind)
	return ""
}

func TestInstallConflictLifecycle(t *testing.T) {
	k := newTestKernel(t)
	v100 := nextDemoZip(t, "1.0.0")
	v110 := nextDemoZip(t, "1.1.0")

	// 安装 1.0.0
	code, body := installZip(t, k, v100, nil)
	if code != fiber.StatusOK || body["status"] != "installed" {
		t.Fatalf("首次安装失败: %d %v", code, body)
	}

	// 内置静态资源路由未被动态路由遮挡
	if code, text := getText(t, k, "/apps/stub/index.html"); code != 200 || text != "builtin-home" {
		t.Fatalf("内置资源异常: %d %q", code, text)
	}

	// Next.js 导出的多种路径形态
	cases := map[string]string{
		"/apps/nextdemo":                            "NEXT HOME v1.0.0",
		"/apps/nextdemo/":                           "NEXT HOME v1.0.0",
		"/apps/nextdemo/about":                      "ABOUT HTML",
		"/apps/nextdemo/about/":                     "ABOUT HTML",
		"/apps/nextdemo/_next/static/chunks/app.js": "__NEXT_DEMO__",
		"/apps/nextdemo/no-such-page":               "NEXT HOME v1.0.0", // spa 回退
	}
	for target, want := range cases {
		code, text := getText(t, k, target)
		if code != 200 || !strings.Contains(text, want) {
			t.Fatalf("%s => %d %q，期望包含 %q", target, code, text, want)
		}
	}

	// 重复安装：409 冲突
	code, body = installZip(t, k, v100, nil)
	if code != fiber.StatusConflict || body["conflict"] != true {
		t.Fatalf("重复安装应返回 409: %d %v", code, body)
	}
	if body["relation"] != "same" {
		t.Fatalf("同版本 relation 应为 same，实际 %v", body["relation"])
	}
	actions, _ := body["actions"].([]any)
	if len(actions) != 3 {
		t.Fatalf("actions 应为 3 项，实际 %v", actions)
	}
	incoming, _ := body["incoming"].(map[string]any)
	if incoming["dir"] != nil && incoming["dir"] != "" {
		t.Fatalf("冲突响应不应暴露暂存目录: %v", incoming["dir"])
	}

	// 新版本冲突提示升级
	code, body = installZip(t, k, v110, nil)
	if code != fiber.StatusConflict || body["relation"] != "newer" {
		t.Fatalf("升级冲突提示异常: %d %v", code, body)
	}

	// 覆盖安装保留数据目录
	marker := filepath.Join(k.dataDir, "apps", "nextdemo", "marker.txt")
	if err := os.WriteFile(marker, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body = installZip(t, k, v110, map[string]string{"action": "overwrite"})
	if code != fiber.StatusOK || body["status"] != "installed" {
		t.Fatalf("覆盖安装失败: %d %v", code, body)
	}
	if code, text := getText(t, k, "/apps/nextdemo"); code != 200 || !strings.Contains(text, "v1.1.0") {
		t.Fatalf("覆盖后内容未更新: %d %q", code, text)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "keep-me" {
		t.Fatalf("数据目录未保留: %v %q", err, raw)
	}

	// 保留现有：旧包被拒绝
	code, body = installZip(t, k, nextDemoZip(t, "0.9.0"), map[string]string{"action": "keep"})
	if code != fiber.StatusOK || body["status"] != "kept" {
		t.Fatalf("保留现有失败: %d %v", code, body)
	}
	if code, text := getText(t, k, "/apps/nextdemo"); code != 200 || !strings.Contains(text, "v1.1.0") {
		t.Fatalf("保留后内容被改动: %d %q", code, text)
	}

	// 另存为新插件
	code, body = installZip(t, k, nextDemoZip(t, "0.9.0"), map[string]string{"action": "copy", "id": "nextdemo-copy"})
	if code != fiber.StatusOK {
		t.Fatalf("另存失败: %d %v", code, body)
	}
	if code, text := getText(t, k, "/apps/nextdemo-copy"); code != 200 || !strings.Contains(text, "v0.9.0") {
		t.Fatalf("另存副本内容异常: %d %q", code, text)
	}

	// 列表包含内置、已安装与副本
	resp, raw := doJSON(t, k, http.MethodGet, "/api/plugins", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("列表接口异常: %d", resp.StatusCode)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, item := range list {
		ids[item["id"].(string)] = item["source"].(string)
	}
	for _, want := range []string{"stub", "nextdemo", "nextdemo-copy"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("列表缺少 %s: %v", want, ids)
		}
	}
	if ids["stub"] != "builtin" || ids["nextdemo"] != "installed" {
		t.Fatalf("source 标记异常: %v", ids)
	}

	// 卸载后路由失效，数据目录保留
	resp, raw = doJSON(t, k, http.MethodDelete, "/api/plugins/nextdemo", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("卸载失败: %d %s", resp.StatusCode, raw)
	}
	if code, _ := getText(t, k, "/apps/nextdemo"); code != fiber.StatusNotFound {
		t.Fatalf("卸载后仍可访问: %d", code)
	}
	if _, err := os.Stat(filepath.Join(k.dataDir, "apps", "nextdemo")); err != nil {
		t.Fatalf("卸载后数据目录应保留: %v", err)
	}
	// 内置插件不可卸载
	resp, _ = doJSON(t, k, http.MethodDelete, "/api/plugins/stub", nil, "")
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("内置插件卸载应返回 403，实际 %d", resp.StatusCode)
	}
}

func TestInstallRejectsInvalidPackages(t *testing.T) {
	k := newTestKernel(t)
	for _, kind := range []string{"badicon", "badspec", "nomanifest", "slip"} {
		code, body := installZip(t, k, invalidZip(t, kind), nil)
		if code != fiber.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d %v", kind, code, body)
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Fatalf("%s 错误响应缺少 error 字段: %v", kind, body)
		}
		t.Logf("%s -> %v", kind, body["error"])
	}
}

func TestInstallIconOutsideStaticRoot(t *testing.T) {
	k := newTestKernel(t)
	zipPath := makeZip(t, filepath.Join(t.TempDir(), "rooticon.zip"), map[string]string{
		"plugin.json":    `{"id":"rooticon","name":"Root Icon","version":"1.0.0","icon":"logo.svg","entry":"index.html","specVersion":1}`,
		"logo.svg":       `<svg xmlns="http://www.w3.org/2000/svg"><rect width="8" height="8"/></svg>`,
		"web/index.html": `<h1>root icon</h1>`,
	})

	code, body := installZip(t, k, zipPath, nil)
	if code != fiber.StatusOK {
		t.Fatalf("安装失败: %d %v", code, body)
	}
	resp, raw := doJSON(t, k, http.MethodGet, "/api/plugins", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("列表接口异常: %d", resp.StatusCode)
	}
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	var icon string
	for _, item := range list {
		if item["id"] == "rooticon" {
			icon, _ = item["icon"].(string)
		}
	}
	if icon != "/apps/rooticon/__icon" {
		t.Fatalf("根目录图标应规范化为 __icon，实际 %q", icon)
	}
	code, text := getText(t, k, "/apps/rooticon/__icon")
	if code != 200 || !strings.Contains(text, "<svg") {
		t.Fatalf("图标读取失败: %d %q", code, text)
	}
}
