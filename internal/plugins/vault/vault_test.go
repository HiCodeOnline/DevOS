package vault

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

const testPassword = "vault-pass-123"

type client struct {
	t     *testing.T
	app   *fiber.App
	token string
}

func newClient(t *testing.T) *client {
	t.Helper()
	dir := t.TempDir()
	api := New()
	api.kdfN = 1 << 12
	if err := api.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	app := fiber.New()
	api.Register(app.Group("/api/apps/vault"))
	return &client{t: t, app: app}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("序列化请求失败: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, "/api/apps/vault"+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-Vault-Token", c.token)
	}
	resp, err := c.app.Test(req, -1)
	if err != nil {
		c.t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return resp.StatusCode, payload
}

func (c *client) setup() {
	c.t.Helper()
	status, payload := c.do("POST", "/setup", map[string]any{
		"password": testPassword, "groupName": "默认分组",
	})
	if status != fiber.StatusOK {
		c.t.Fatalf("设置密码失败: %d %v", status, payload)
	}
	token, _ := payload["token"].(string)
	if token == "" {
		c.t.Fatal("未返回解锁令牌")
	}
	c.token = token
}

func snapshotOf(payload map[string]any) (groups []any, secrets []any, tags []any) {
	snapshot := payload
	if wrapped, ok := payload["snapshot"].(map[string]any); ok {
		snapshot = wrapped
	}
	if snapshot == nil {
		return nil, nil, nil
	}
	groups, _ = snapshot["groups"].([]any)
	secrets, _ = snapshot["secrets"].([]any)
	tags, _ = snapshot["tags"].([]any)
	return
}

func TestSetupAndUnlockFlow(t *testing.T) {
	c := newClient(t)

	status, payload := c.do("GET", "/state", nil)
	if status != fiber.StatusOK || payload["initialized"] != false {
		t.Fatalf("初始状态应为未初始化: %d %v", status, payload)
	}
	if status, _ := c.do("GET", "/data", nil); status != fiber.StatusUnauthorized {
		t.Fatalf("未解锁访问数据应返回 401，实际 %d", status)
	}
	if status, _ := c.do("POST", "/setup", map[string]any{"password": "short"}); status != fiber.StatusBadRequest {
		t.Fatalf("弱密码应被拒绝，实际 %d", status)
	}
	c.setup()
	if status, _ := c.do("POST", "/setup", map[string]any{"password": testPassword}); status != fiber.StatusConflict {
		t.Fatalf("重复初始化应返回 409，实际 %d", status)
	}

	status, payload = c.do("GET", "/state", nil)
	if status != fiber.StatusOK || payload["initialized"] != true {
		t.Fatalf("初始化后状态错误: %d %v", status, payload)
	}
	status, payload = c.do("GET", "/data", nil)
	groups, secrets, _ := snapshotOf(payload)
	if status != fiber.StatusOK || len(groups) != 1 || len(secrets) != 0 {
		t.Fatalf("初始化后应有默认分组: %d %v", status, payload)
	}
	groupID, _ := groups[0].(map[string]any)["id"].(string)

	status, payload = c.do("POST", "/secrets", map[string]any{
		"groupId": groupID, "name": "GitHub", "key": "octocat",
		"value": "ghp_super_secret", "note": "个人访问令牌",
		"tags": []string{"dev", "生产", "dev"},
	})
	_, secrets, tags := snapshotOf(payload)
	if status != fiber.StatusOK || len(secrets) != 1 {
		t.Fatalf("创建条目失败: %d %v", status, payload)
	}
	created := secrets[0].(map[string]any)
	if created["name"] != "GitHub" || created["key"] != "octocat" || created["value"] != "ghp_super_secret" {
		t.Fatalf("条目字段不正确: %v", created)
	}
	if got := created["tags"].([]any); len(got) != 2 {
		t.Fatalf("标签应去重为 2 个: %v", got)
	}
	if len(tags) != 2 {
		t.Fatalf("标签统计应为 2 个: %v", tags)
	}

	// 重启后使用新密码解锁，验证密文落盘。
	c.token = ""
	if status, _ := c.do("POST", "/unlock", map[string]any{"password": "wrong-password"}); status != fiber.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401，实际 %d", status)
	}
}

func TestPersistenceAndChangePassword(t *testing.T) {
	dir := t.TempDir()
	first := New()
	first.kdfN = 1 << 12
	if err := first.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	app := fiber.New()
	first.Register(app.Group("/api/apps/vault"))

	do := func(method, path string, body any, token string) (int, map[string]any) {
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		}
		req := httptest.NewRequest(method, "/api/apps/vault"+path, reader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Vault-Token", token)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		return resp.StatusCode, payload
	}

	status, payload := do("POST", "/setup", map[string]any{"password": testPassword}, "")
	if status != fiber.StatusOK {
		t.Fatalf("设置密码失败: %d %v", status, payload)
	}
	token, _ := payload["token"].(string)
	groups, _, _ := snapshotOf(payload)
	groupID := groups[0].(map[string]any)["id"].(string)

	status, payload = do("POST", "/secrets", map[string]any{
		"groupId": groupID, "name": "数据库", "key": "root", "value": "p@ssw0rd", "tags": []string{"db"},
	}, token)
	if status != fiber.StatusOK {
		t.Fatalf("创建条目失败: %d %v", status, payload)
	}
	_, secrets, _ := snapshotOf(payload)
	secretID := secrets[0].(map[string]any)["id"].(string)

	// 磁盘上不应出现明文，且应带加密前缀。
	raw, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil {
		t.Fatalf("读取数据文件失败: %v", err)
	}
	if !strings.HasPrefix(string(raw), storePrefix) {
		t.Fatalf("数据文件未加密: %s", raw)
	}
	if strings.Contains(string(raw), "p@ssw0rd") || strings.Contains(string(raw), "数据库") {
		t.Fatal("数据文件中出现明文")
	}
	metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("读取元信息失败: %v", err)
	}
	if strings.Contains(string(metaRaw), testPassword) {
		t.Fatal("元信息中不应包含密码明文")
	}

	// 模拟应用重启。
	second := New()
	second.kdfN = 1 << 12
	if err := second.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("重启初始化失败: %v", err)
	}
	app2 := fiber.New()
	second.Register(app2.Group("/api/apps/vault"))
	do2 := func(method, path string, body any, token string) (int, map[string]any) {
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		}
		req := httptest.NewRequest(method, "/api/apps/vault"+path, reader)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Vault-Token", token)
		}
		resp, err := app2.Test(req, -1)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		return resp.StatusCode, payload
	}

	if status, _ := do2("POST", "/unlock", map[string]any{"password": "not-the-password"}, ""); status != fiber.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401，实际 %d", status)
	}
	status, payload = do2("POST", "/unlock", map[string]any{"password": testPassword}, "")
	if status != fiber.StatusOK {
		t.Fatalf("解锁失败: %d %v", status, payload)
	}
	token, _ = payload["token"].(string)
	_, secrets, _ = snapshotOf(payload)
	if len(secrets) != 1 || secrets[0].(map[string]any)["value"] != "p@ssw0rd" {
		t.Fatalf("重启后数据不一致: %v", payload)
	}

	// 修改密码后旧密码失效、新密码可用，条目保持不变。
	status, payload = do2("POST", "/password", map[string]any{
		"oldPassword": "bad-old", "newPassword": "new-vault-pass-456",
	}, token)
	if status != fiber.StatusUnauthorized {
		t.Fatalf("旧密码错误应返回 401，实际 %d", status)
	}
	status, payload = do2("POST", "/password", map[string]any{
		"oldPassword": testPassword, "newPassword": "new-vault-pass-456",
	}, token)
	if status != fiber.StatusOK {
		t.Fatalf("修改密码失败: %d %v", status, payload)
	}
	newToken, _ := payload["token"].(string)
	if newToken == "" || newToken == token {
		t.Fatal("修改密码后应返回新的解锁令牌")
	}
	if status, _ := do2("POST", "/unlock", map[string]any{"password": testPassword}, ""); status != fiber.StatusUnauthorized {
		t.Fatalf("旧密码应失效，实际 %d", status)
	}
	status, payload = do2("POST", "/unlock", map[string]any{"password": "new-vault-pass-456"}, "")
	if status != fiber.StatusOK {
		t.Fatalf("新密码解锁失败: %d %v", status, payload)
	}
	token, _ = payload["token"].(string)
	_, secrets, _ = snapshotOf(payload)
	if len(secrets) != 1 || secrets[0].(map[string]any)["value"] != "p@ssw0rd" {
		t.Fatalf("修改密码后数据丢失: %v", payload)
	}

	// 锁定后旧令牌失效，但重新解锁可恢复。
	if status, _ := do2("POST", "/lock", nil, token); status != fiber.StatusOK {
		t.Fatalf("锁定失败: %d", status)
	}
	if status, _ := do2("GET", "/data", nil, token); status != fiber.StatusUnauthorized {
		t.Fatalf("锁定后应返回 401，实际 %d", status)
	}

	status, payload = do2("POST", "/unlock", map[string]any{"password": "new-vault-pass-456"}, "")
	if status != fiber.StatusOK {
		t.Fatalf("重新解锁失败: %d %v", status, payload)
	}
	token, _ = payload["token"].(string)
	_, secrets, _ = snapshotOf(payload)
	secretID = secrets[0].(map[string]any)["id"].(string)

	// 删除条目。
	if status, payload = do2("DELETE", "/secrets/"+secretID, nil, token); status != fiber.StatusOK {
		t.Fatalf("删除条目失败: %d %v", status, payload)
	}
	_, secrets, _ = snapshotOf(payload)
	if len(secrets) != 0 {
		t.Fatalf("删除后仍存在条目: %v", secrets)
	}
}

func TestGroupManagement(t *testing.T) {
	c := newClient(t)
	c.setup()

	_, payload := c.do("GET", "/data", nil)
	groups, _, _ := snapshotOf(payload)
	defaultID := groups[0].(map[string]any)["id"].(string)

	status, payload := c.do("POST", "/groups", map[string]any{"name": "生产环境", "color": "#10b981"})
	groups, _, _ = snapshotOf(payload)
	if status != fiber.StatusOK || len(groups) != 2 {
		t.Fatalf("创建分组失败: %d %v", status, payload)
	}
	group := groups[1].(map[string]any)
	groupID := group["id"].(string)
	if group["name"] != "生产环境" || group["color"] != "#10b981" || group["order"] != float64(1) {
		t.Fatalf("分组字段不正确: %v", group)
	}

	status, payload = c.do("PUT", "/groups/"+groupID, map[string]any{"name": "线上", "color": "#f43f5e"})
	groups, _, _ = snapshotOf(payload)
	if status != fiber.StatusOK || groups[1].(map[string]any)["name"] != "线上" {
		t.Fatalf("更新分组失败: %d %v", status, payload)
	}

	// 条目归入自定义分组，删除分组后应迁移到剩余分组。
	status, payload = c.do("POST", "/secrets", map[string]any{
		"groupId": groupID, "name": "API Key", "value": "secret", "tags": []string{"api"},
	})
	_, secrets, _ := snapshotOf(payload)
	if status != fiber.StatusOK {
		t.Fatalf("创建条目失败: %d %v", status, payload)
	}
	if secrets[0].(map[string]any)["groupId"] != groupID {
		t.Fatalf("条目未归入指定分组: %v", secrets[0])
	}

	status, payload = c.do("DELETE", "/groups/"+groupID, nil)
	groups, secrets, _ = snapshotOf(payload)
	if status != fiber.StatusOK || len(groups) != 1 {
		t.Fatalf("删除分组失败: %d %v", status, payload)
	}
	if secrets[0].(map[string]any)["groupId"] != defaultID {
		t.Fatalf("删除分组后条目未迁移: %v", secrets[0])
	}
	if status, _ := c.do("DELETE", "/groups/"+defaultID, nil); status != fiber.StatusBadRequest {
		t.Fatalf("删除最后一个分组应返回 400，实际 %d", status)
	}

	// 标签检索所需的数据统计保持正确。
	_, payload = c.do("GET", "/data", nil)
	_, _, tags := snapshotOf(payload)
	if len(tags) != 1 || tags[0].(map[string]any)["name"] != "api" || tags[0].(map[string]any)["count"] != float64(1) {
		t.Fatalf("标签统计不正确: %v", tags)
	}

	if status, _ := c.do("PUT", "/groups/missing", map[string]any{"name": "x"}); status != fiber.StatusNotFound {
		t.Fatalf("更新不存在的分组应返回 404，实际 %d", status)
	}
}

func TestValidation(t *testing.T) {
	c := newClient(t)
	c.setup()

	if status, _ := c.do("POST", "/secrets", map[string]any{"name": "   "}); status != fiber.StatusBadRequest {
		t.Fatalf("空名称应返回 400，实际 %d", status)
	}
	if status, _ := c.do("POST", "/groups", map[string]any{"name": ""}); status != fiber.StatusBadRequest {
		t.Fatalf("空分组名应返回 400，实际 %d", status)
	}
	if status, _ := c.do("POST", "/groups", map[string]any{"name": "X", "color": "not-a-color"}); status != fiber.StatusOK {
		t.Fatalf("非法颜色应回退而不是报错，实际 %d", status)
	}
	if status, _ := c.do("POST", "/password", map[string]any{"oldPassword": testPassword, "newPassword": "123"}); status != fiber.StatusBadRequest {
		t.Fatalf("弱新密码应返回 400，实际 %d", status)
	}
	if status, _ := c.do("DELETE", "/secrets/missing", nil); status != fiber.StatusNotFound {
		t.Fatalf("删除不存在的条目应返回 404，实际 %d", status)
	}
}

func TestVaultRoutesAreIsolated(t *testing.T) {
	c := newClient(t)
	if status, _ := c.do("POST", "/secretz", map[string]any{}); status != fiber.StatusNotFound && status != fiber.StatusMethodNotAllowed {
		t.Fatalf("未知路由不应成功，实际 %d", status)
	}
}
