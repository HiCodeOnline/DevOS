package dbapp

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

func newTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	p := New()
	if err := p.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, filepath.Join(dir, "connections.db")
}

// readDBFiles 读取数据库主文件与 WAL，确保新写入的密文也被检查到。
func readDBFiles(t *testing.T, path string) []byte {
	t.Helper()
	var out []byte
	for _, name := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("读取数据库 %s: %v", name, err)
		}
		out = append(out, data...)
	}
	return out
}

func TestConnectionsEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "connections.json")
	seed := `[{"id":"c1","name":"生产库","driver":"mysql","host":"10.0.0.1","port":3306,"user":"root","password":"db-pass-123"}]`
	if err := os.WriteFile(legacy, []byte(seed), 0o600); err != nil {
		t.Fatalf("写入初始数据: %v", err)
	}

	p := New()
	if err := p.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Close()

	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Fatalf("历史 JSON 未保留备份: %v", err)
	}
	raw := readDBFiles(t, filepath.Join(dir, "connections.db"))
	if strings.Contains(string(raw), "db-pass-123") {
		t.Fatalf("数据库中仍存在明文密码")
	}
	if !strings.Contains(string(raw), "enc:v1:") {
		t.Fatalf("密码未加密落盘")
	}

	conn, err := p.conn("c1")
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	if conn.Password != "db-pass-123" {
		t.Fatalf("内存密码未正确解密 = %q", conn.Password)
	}

	// 模拟重启：新的实例重新读取数据库。
	reopened := New()
	if err := reopened.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("重新 Init: %v", err)
	}
	defer reopened.Close()
	again, err := reopened.conn("c1")
	if err != nil || again.Password != "db-pass-123" {
		t.Fatalf("重启后解密失败: %#v, err=%v", again, err)
	}
}

func TestExportImportRequiresKey(t *testing.T) {
	p, path := newTestDB(t)
	if _, err := p.saveConnection(Connection{Name: "本地", Driver: "mysql", Host: "127.0.0.1", Port: 3306, User: "root", Password: "secret-xyz"}); err != nil {
		t.Fatalf("saveConnection: %v", err)
	}

	raw := readDBFiles(t, path)
	if strings.Contains(string(raw), "secret-xyz") || !strings.Contains(string(raw), "enc:v1:") {
		t.Fatalf("落盘内容未正确加密")
	}

	app := fiber.New()
	p.Register(app.Group("/api"))

	resp, err := app.Test(httptest.NewRequest("POST", "/api/connections/export", nil), -1)
	if err != nil {
		t.Fatalf("导出请求: %v", err)
	}
	var exported struct {
		Filename string `json:"filename"`
		Key      string `json:"key"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&exported); err != nil {
		t.Fatalf("解析导出结果: %v", err)
	}
	if exported.Key == "" || exported.Content == "" || exported.Filename == "" {
		t.Fatalf("导出结果缺少字段: %+v", exported)
	}
	if !strings.Contains(exported.Content, "devos-secure-export") {
		t.Fatalf("导出内容不是加密信封: %s", exported.Content)
	}

	importBody := func(key string) int {
		body, _ := json.Marshal(map[string]string{"content": exported.Content, "key": key})
		req := httptest.NewRequest("POST", "/api/connections/import", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("导入请求: %v", err)
		}
		return res.StatusCode
	}

	if code := importBody(""); code != fiber.StatusBadRequest {
		t.Fatalf("未填密钥导入应失败，状态码 = %d", code)
	}
	if code := importBody("AAAA-BBBB-CCCC-DDDD-EEEE-FFFF"); code != fiber.StatusBadRequest {
		t.Fatalf("错误密钥导入应失败，状态码 = %d", code)
	}
	if code := importBody(exported.Key); code != fiber.StatusOK {
		t.Fatalf("正确密钥导入应成功，状态码 = %d", code)
	}
	if n := len(p.listConnections()); n != 2 {
		t.Fatalf("导入后连接数量 = %d，期望 2", n)
	}
}
