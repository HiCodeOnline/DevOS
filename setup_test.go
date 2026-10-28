package main

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/datadir"
)

func TestResolveDataDirFirstRunAndSavedLocation(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AppData", base)

	// 首次启动：目录为空时需要进入初始化向导。
	dir, needSetup, err := resolveDataDir(false)
	if err != nil {
		t.Fatalf("resolveDataDir: %v", err)
	}
	if !needSetup {
		t.Fatal("首次启动应要求选择数据目录")
	}
	if dir != filepath.Join(base, "DevOS") {
		t.Fatalf("默认目录不匹配: %s", dir)
	}

	// 已保存选择：直接使用保存的目录。
	custom := filepath.Join(base, "custom-data")
	if err := datadir.WriteLocation(custom); err != nil {
		t.Fatalf("写入位置记录: %v", err)
	}
	dir, needSetup, err = resolveDataDir(false)
	if err != nil {
		t.Fatalf("resolveDataDir: %v", err)
	}
	if needSetup || dir != custom {
		t.Fatalf("应使用已保存的数据目录，实际 %s（needSetup=%v）", dir, needSetup)
	}

	// 强制初始化：即使已有记录也进入向导。
	if _, needSetup, _ = resolveDataDir(true); !needSetup {
		t.Fatal("-setup 应强制进入初始化向导")
	}
}

func TestResolveDataDirMigratesLegacyInstall(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AppData", base)
	root := filepath.Join(base, "DevOS")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("创建目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("写入旧配置: %v", err)
	}

	dir, needSetup, err := resolveDataDir(false)
	if err != nil {
		t.Fatalf("resolveDataDir: %v", err)
	}
	if needSetup || dir != root {
		t.Fatalf("旧版本安装应直接使用默认目录，实际 %s（needSetup=%v）", dir, needSetup)
	}
	if _, err := os.Stat(filepath.Join(root, "location.json")); err != nil {
		t.Fatalf("应自动写入位置记录: %v", err)
	}
}

func TestSetupAppChooseDirectory(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	web := fstest.MapFS{
		"setup.html": &fstest.MapFile{Data: []byte("<!doctype html><title>setup</title>")},
	}
	ready := make(chan string, 1)
	app, err := newSetupApp(web, setupOptions{
		BaseURL:    "http://127.0.0.1:7788",
		DefaultDir: t.TempDir(),
		OnReady: func(dir string) {
			ready <- dir
		},
	})
	if err != nil {
		t.Fatalf("newSetupApp: %v", err)
	}

	code, page := setupRequest(t, app, "GET", "/", "", nil)
	if code != 200 || !strings.Contains(page, "setup") {
		t.Fatalf("初始化页面异常: %d %s", code, page)
	}

	// 缺少请求标识应被拒绝。
	code, _ = setupRequest(t, app, "POST", "/api/setup/choose", `{"dir":"x"}`, nil)
	if code != 400 {
		t.Fatalf("缺少请求标识应返回 400，实际 %d", code)
	}

	target := filepath.Join(t.TempDir(), "devos-data")
	body, _ := json.Marshal(map[string]string{"dir": target})
	code, resp := setupRequest(t, app, "POST", "/api/setup/choose", string(body), map[string]string{
		"X-DevOS-Request": "1",
		"Content-Type":    "application/json",
	})
	if code != 200 {
		t.Fatalf("选择目录失败: %d %s", code, resp)
	}
	select {
	case got := <-ready:
		if got != target {
			t.Fatalf("回调目录不匹配: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未触发初始化完成回调")
	}

	location, err := datadir.LocationPath()
	if err != nil {
		t.Fatalf("位置记录路径: %v", err)
	}
	raw, err := os.ReadFile(location)
	if err != nil {
		t.Fatalf("读取位置记录: %v", err)
	}
	var loc struct {
		DataDir string `json:"dataDir"`
	}
	if err := json.Unmarshal(raw, &loc); err != nil {
		t.Fatalf("解析位置记录: %v", err)
	}
	if loc.DataDir != target {
		t.Fatalf("位置记录目录不匹配: %s", loc.DataDir)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("数据目录未创建: %v", err)
	}
}

// setupRequest 向初始化向导发起请求，返回状态码与响应内容。
func setupRequest(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return res.StatusCode, string(data)
}
