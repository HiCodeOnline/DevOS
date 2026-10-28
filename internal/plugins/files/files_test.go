package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

type client struct {
	t   *testing.T
	app *fiber.App
	dir string
}

func newClient(t *testing.T) *client {
	t.Helper()
	dir := t.TempDir()
	api := New()
	if err := api.Init(&plugin.Context{DataDir: dir, Logger: slog.Default()}); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })
	app := fiber.New()
	api.Register(app.Group("/api/apps/files"))
	return &client{t: t, app: app, dir: dir}
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
	req := httptest.NewRequest(method, "/api/apps/files"+path, reader)
	req.Header.Set("Content-Type", "application/json")
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

func (c *client) doRaw(method, path string) (int, http.Header, []byte) {
	c.t.Helper()
	req := httptest.NewRequest(method, "/api/apps/files"+path, nil)
	resp, err := c.app.Test(req, -1)
	if err != nil {
		c.t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func writeSource(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	return path
}

func shaOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func fileOf(payload map[string]any) map[string]any {
	file, _ := payload["file"].(map[string]any)
	return file
}

func groupIDOf(payload map[string]any) string {
	groups, _ := payload["groups"].([]any)
	if len(groups) == 0 {
		return ""
	}
	group, _ := groups[0].(map[string]any)
	id, _ := group["id"].(string)
	return id
}

func TestAddFileStoresByDateAndDedupes(t *testing.T) {
	c := newClient(t)
	status, meta := c.do("GET", "/meta", nil)
	if status != fiber.StatusOK {
		t.Fatalf("读取元信息失败: %d %v", status, meta)
	}
	groupID := groupIDOf(meta)
	if groupID == "" {
		t.Fatal("初始化后应有默认分组")
	}
	if meta["storageDir"] == "" {
		t.Fatal("应返回文件存放目录")
	}

	srcDir := t.TempDir()
	content := []byte("hello devos")
	src := writeSource(t, srcDir, "季度报告.txt", content)

	status, payload := c.do("POST", "/add", map[string]any{
		"path": src, "groupId": groupID, "date": "2025-03-07",
		"tags": []string{"工作", "重要", "工作"}, "keepSource": true, "note": "季度总结",
	})
	if status != fiber.StatusOK {
		t.Fatalf("添加文件失败: %d %v", status, payload)
	}
	file := fileOf(payload)
	if file["name"] != "季度报告.txt" || file["sha256"] != shaOf(content) {
		t.Fatalf("文件字段不正确: %v", file)
	}
	if tags, _ := file["tags"].([]any); len(tags) != 2 {
		t.Fatalf("标签应去重为 2 个: %v", file["tags"])
	}
	rel, _ := file["path"].(string)
	if !strings.HasPrefix(rel, "2025/03/07/") {
		t.Fatalf("文件路径应按年/月/日存储，实际 %s", rel)
	}
	blob := filepath.Join(c.dir, "storage", filepath.FromSlash(rel))
	if data, err := os.ReadFile(blob); err != nil || !bytes.Equal(data, content) {
		t.Fatalf("归档文件内容不一致: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("保留原文件时源文件不应被删除: %v", err)
	}
	if file["keepSource"] != true || file["kind"] != "text" {
		t.Fatalf("记录字段不正确: %v", file)
	}

	// 内容相同的文件应提示重复。
	status, payload = c.do("POST", "/add", map[string]any{
		"path": src, "groupId": groupID, "date": "2025-03-07", "keepSource": true,
	})
	if status != fiber.StatusConflict || payload["code"] != "duplicate" {
		t.Fatalf("重复文件应返回 409 duplicate: %d %v", status, payload)
	}
	existing, _ := payload["existing"].(map[string]any)
	if existing["id"] != file["id"] {
		t.Fatalf("重复提示应返回已有记录: %v", payload)
	}

	// 明确仍然添加：复用已有副本，不重复复制，并删除不保留的源文件。
	src2 := writeSource(t, srcDir, "季度报告-副本.txt", content)
	status, payload = c.do("POST", "/add", map[string]any{
		"path": src2, "groupId": groupID, "date": "2025-03-07",
		"tags": []string{"重要"}, "keepSource": false, "force": true,
	})
	if status != fiber.StatusOK {
		t.Fatalf("强制添加失败: %d %v", status, payload)
	}
	second := fileOf(payload)
	if second["path"] != file["path"] {
		t.Fatalf("强制添加应复用已有副本: %v", second["path"])
	}
	if second["keepSource"] != false {
		t.Fatalf("keepSource 字段不正确: %v", second)
	}
	if _, err := os.Stat(src2); !os.IsNotExist(err) {
		t.Fatalf("不保留原文件时源文件应被删除: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(blob))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("强制添加不应产生新的副本，实际 %d 个文件", len(entries))
	}

	status, payload = c.do("GET", "/list?groupId="+groupID, nil)
	if status != fiber.StatusOK || payload["count"] != float64(2) {
		t.Fatalf("分组内应有 2 条记录: %d %v", status, payload)
	}
	status, payload = c.do("GET", "/list?q=季度", nil)
	if status != fiber.StatusOK || payload["count"] != float64(2) {
		t.Fatalf("搜索应命中 2 条记录: %d %v", status, payload)
	}
	status, payload = c.do("GET", "/list?tag=重要", nil)
	if status != fiber.StatusOK || payload["count"] != float64(2) {
		t.Fatalf("标签筛选应命中 2 条记录: %d %v", status, payload)
	}
}

func TestKeepSourceFalseRemovesSource(t *testing.T) {
	c := newClient(t)
	src := writeSource(t, t.TempDir(), "install.iso", []byte("iso-content"))
	status, payload := c.do("POST", "/add", map[string]any{
		"path": src, "keepSource": false,
	})
	if status != fiber.StatusOK {
		t.Fatalf("添加文件失败: %d %v", status, payload)
	}
	file := fileOf(payload)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源文件应被删除: %v", err)
	}
	if _, err := os.Stat(file["absPath"].(string)); err != nil {
		t.Fatalf("归档副本应存在: %v", err)
	}
	if file["kind"] != "archive" || file["mime"] == "" {
		t.Fatalf("ISO 应识别为压缩包类型: %v", file)
	}
	// 未指定日期时默认当日。
	if file["date"] == "" {
		t.Fatalf("未指定日期时应默认当日: %v", file)
	}
}

func TestPreviewAndDownload(t *testing.T) {
	c := newClient(t)

	textContent := []byte("你好，文件库")
	textSrc := writeSource(t, t.TempDir(), "说明.md", textContent)
	status, payload := c.do("POST", "/add", map[string]any{"path": textSrc, "keepSource": true})
	if status != fiber.StatusOK {
		t.Fatalf("添加文本文件失败: %d %v", status, payload)
	}
	textID := fileOf(payload)["id"].(string)

	status, header, body := c.doRaw("GET", "/files/"+textID+"/raw")
	if status != fiber.StatusOK || !bytes.Equal(body, textContent) {
		t.Fatalf("文本预览失败: %d %q", status, body)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "text/") {
		t.Fatalf("文本预览 Content-Type 不正确: %s", ct)
	}
	status, header, _ = c.doRaw("GET", "/files/"+textID+"/raw?download=1")
	if status != fiber.StatusOK || !strings.Contains(header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("下载应返回 attachment: %d %v", status, header)
	}

	imageContent := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3, 4}
	imageSrc := writeSource(t, t.TempDir(), "封面.png", imageContent)
	status, payload = c.do("POST", "/add", map[string]any{"path": imageSrc, "keepSource": true})
	if status != fiber.StatusOK {
		t.Fatalf("添加图片失败: %d %v", status, payload)
	}
	image := fileOf(payload)
	if image["kind"] != "image" {
		t.Fatalf("PNG 应识别为图片: %v", image)
	}
	status, header, body = c.doRaw("GET", "/files/"+image["id"].(string)+"/raw")
	if status != fiber.StatusOK || !bytes.Equal(body, imageContent) {
		t.Fatalf("图片预览内容不一致: %d %v", status, body)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("图片 Content-Type 不正确: %s", ct)
	}
}

func TestGroupsFiltersAndUpdate(t *testing.T) {
	c := newClient(t)
	status, payload := c.do("POST", "/groups", map[string]any{"name": "ISO 镜像", "color": "#6366f1"})
	if status != fiber.StatusOK {
		t.Fatalf("创建分组失败: %d %v", status, payload)
	}
	group := payload["group"].(map[string]any)
	groupID := group["id"].(string)

	if status, _ := c.do("POST", "/groups", map[string]any{"name": "ISO 镜像"}); status != fiber.StatusConflict {
		t.Fatalf("同名分组应返回 409，实际 %d", status)
	}
	if status, _ := c.do("PUT", "/groups/"+groupID, map[string]any{"name": "镜像库", "color": "#10b981"}); status != fiber.StatusOK {
		t.Fatalf("更新分组失败: %d", status)
	}

	src := writeSource(t, t.TempDir(), "系统镜像.iso", []byte("image-data"))
	status, payload = c.do("POST", "/add", map[string]any{
		"path": src, "groupId": groupID, "date": "2024-01-02", "keepSource": true,
	})
	if status != fiber.StatusOK {
		t.Fatalf("添加文件失败: %d %v", status, payload)
	}
	file := fileOf(payload)
	fileID := file["id"].(string)

	status, payload = c.do("GET", "/list?groupId="+groupID, nil)
	if status != fiber.StatusOK || payload["count"] != float64(1) {
		t.Fatalf("分组筛选失败: %d %v", status, payload)
	}
	status, payload = c.do("GET", "/list?groupId=none", nil)
	if status != fiber.StatusOK || payload["count"] != float64(0) {
		t.Fatalf("未分组筛选项失败: %d %v", status, payload)
	}

	status, payload = c.do("DELETE", "/groups/"+groupID, nil)
	if status != fiber.StatusOK || payload["moved"] != float64(1) {
		t.Fatalf("删除分组失败: %d %v", status, payload)
	}
	status, payload = c.do("GET", "/meta", nil)
	if status != fiber.StatusOK || payload["ungrouped"] != float64(1) {
		t.Fatalf("删除分组后文件应移动到未分组: %d %v", status, payload)
	}

	status, payload = c.do("PUT", "/files/"+fileID, map[string]any{
		"groupId": "", "date": "2024-02-03", "tags": []string{"系统"}, "note": "测试备注",
	})
	if status != fiber.StatusOK {
		t.Fatalf("更新文件失败: %d %v", status, payload)
	}
	updated := fileOf(payload)
	if updated["groupId"] != "" || updated["date"] != "2024-02-03" || updated["note"] != "测试备注" {
		t.Fatalf("更新字段不正确: %v", updated)
	}

	if status, _ := c.do("POST", "/add", map[string]any{"path": src, "groupId": "not-exist"}); status != fiber.StatusBadRequest {
		t.Fatalf("不存在的分组应返回 400，实际 %d", status)
	}
}

func TestDeleteFileRemovesBlob(t *testing.T) {
	c := newClient(t)
	src := writeSource(t, t.TempDir(), "临时.log", []byte("log-content"))
	status, payload := c.do("POST", "/add", map[string]any{"path": src, "keepSource": true})
	if status != fiber.StatusOK {
		t.Fatalf("添加文件失败: %d %v", status, payload)
	}
	file := fileOf(payload)
	blob := file["absPath"].(string)
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("归档副本应存在: %v", err)
	}
	if status, _ := c.do("DELETE", "/files/"+file["id"].(string), nil); status != fiber.StatusOK {
		t.Fatalf("删除文件失败: %d", status)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Fatalf("无引用时应删除磁盘副本: %v", err)
	}
	status, payload = c.do("GET", "/meta", nil)
	if status != fiber.StatusOK || payload["count"] != float64(0) {
		t.Fatalf("删除后应无记录: %d %v", status, payload)
	}
}

func TestInvalidInput(t *testing.T) {
	c := newClient(t)
	if status, _ := c.do("POST", "/add", map[string]any{"path": ""}); status != fiber.StatusBadRequest {
		t.Fatalf("空路径应返回 400，实际 %d", status)
	}
	if status, _ := c.do("POST", "/add", map[string]any{"path": filepath.Join(t.TempDir(), "missing.txt")}); status != fiber.StatusBadRequest {
		t.Fatalf("不存在的文件应返回 400，实际 %d", status)
	}
	src := writeSource(t, t.TempDir(), "a.txt", []byte("a"))
	if status, _ := c.do("POST", "/add", map[string]any{"path": src, "date": "2025/01/02"}); status != fiber.StatusBadRequest {
		t.Fatalf("非法日期应返回 400，实际 %d", status)
	}
	if status, _, _ := c.doRaw("GET", "/files/not-exist/raw"); status != fiber.StatusNotFound {
		t.Fatalf("不存在的记录应返回 404，实际 %d", status)
	}
}
