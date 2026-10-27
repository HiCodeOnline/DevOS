package notes

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

func newTestAPI(t *testing.T) (*API, *fiber.App) {
	t.Helper()
	// 上传后的静态文件在 Windows 上可能仍被系统句柄占用，这里只做尽力清理，
	// 避免 t.TempDir 的严格清理导致误报失败。
	dir, err := os.MkdirTemp("", "devos-notes-")
	if err != nil {
		t.Fatalf("创建临时目录: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	api := New()
	ctx := &plugin.Context{
		DataDir: dir,
		BaseURL: "http://127.0.0.1:7788",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := api.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	app := fiber.New()
	api.Register(app.Group("/api"))
	return api, app
}

func doJSON(t *testing.T, app *fiber.App, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func createPage(t *testing.T, app *fiber.App, body map[string]any) Page {
	t.Helper()
	status, data := doJSON(t, app, http.MethodPost, "/api/pages", body)
	if status != http.StatusCreated {
		t.Fatalf("创建页面失败: %d %s", status, data)
	}
	var page Page
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("解析页面: %v", err)
	}
	return page
}

func workspace(t *testing.T, app *fiber.App) []PageSummary {
	t.Helper()
	status, data := doJSON(t, app, http.MethodGet, "/api/workspace", nil)
	if status != http.StatusOK {
		t.Fatalf("读取工作区失败: %d", status)
	}
	var out struct {
		Pages []PageSummary `json:"pages"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析工作区: %v", err)
	}
	return out.Pages
}

func findSummary(pages []PageSummary, id string) *PageSummary {
	for i := range pages {
		if pages[i].ID == id {
			return &pages[i]
		}
	}
	return nil
}

func TestInitCreatesEncryptedStore(t *testing.T) {
	api, _ := newTestAPI(t)
	data, err := os.ReadFile(api.file)
	if err != nil {
		t.Fatalf("读取 notes.json: %v", err)
	}
	if !strings.HasPrefix(string(data), "enc:v1:") {
		t.Fatalf("笔记数据未加密落盘: %.40s", data)
	}
	if _, err := os.Stat(api.filesDir); err != nil {
		t.Fatalf("附件目录未创建: %v", err)
	}
}

func TestCreateNormalizesBlocks(t *testing.T) {
	_, app := newTestAPI(t)
	page := createPage(t, app, map[string]any{
		"title": "  第一页  ",
		"blocks": []map[string]any{
			{"id": "../bad", "type": "unknown", "content": `<b onclick="x()">粗体</b><script>alert(1)</script>`},
			{"id": "dup00001", "type": "heading1", "content": "标题"},
			{"id": "dup00001", "type": "heading1", "content": "重复 ID"},
		},
	})
	if page.Title != "第一页" {
		t.Fatalf("标题未去空格: %q", page.Title)
	}
	if len(page.Blocks) != 3 {
		t.Fatalf("块数量错误: %d", len(page.Blocks))
	}
	first := page.Blocks[0]
	if first.Type != "paragraph" {
		t.Fatalf("未知类型应归一为 paragraph: %s", first.Type)
	}
	if validID("../bad") {
		t.Fatal("非法 ID 应被替换")
	}
	if strings.Contains(first.Content, "onclick") || strings.Contains(first.Content, "<script") {
		t.Fatalf("行内标签未清洗: %s", first.Content)
	}
	if !strings.Contains(first.Content, "<b>粗体</b>") {
		t.Fatalf("允许的标签被误删: %s", first.Content)
	}
	if page.Blocks[1].ID == page.Blocks[2].ID {
		t.Fatal("重复块 ID 应被替换")
	}
}

func TestCreateEmptyPageGetsParagraphAndEmptyTitle(t *testing.T) {
	_, app := newTestAPI(t)
	page := createPage(t, app, map[string]any{})
	if page.Title != "" {
		t.Fatalf("空标题应保持为空以便前端按语言显示占位: %q", page.Title)
	}
	if len(page.Blocks) != 1 || page.Blocks[0].Type != "paragraph" {
		t.Fatalf("空页面应补一个空段落: %+v", page.Blocks)
	}
}

func TestSavePageSanitizesAndUpdatesTimestamps(t *testing.T) {
	_, app := newTestAPI(t)
	page := createPage(t, app, map[string]any{"title": "旧标题"})

	blocks := []map[string]any{
		{"id": "aaaa0001", "type": "code", "content": "<script>keep</script>", "props": map[string]any{"language": "go"}},
		{"id": "aaaa0002", "type": "image", "props": map[string]any{"url": "javascript:alert(1)"}},
		{"id": "aaaa0003", "type": "table", "props": map[string]any{"rows": [][]string{{"a", "b"}}, "hasHeader": true}},
	}
	status, data := doJSON(t, app, http.MethodPut, "/api/pages/"+page.ID, map[string]any{
		"title":  "  新标题  ",
		"blocks": blocks,
	})
	if status != http.StatusOK {
		t.Fatalf("保存失败: %d %s", status, data)
	}
	var saved Page
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Title != "新标题" {
		t.Fatalf("标题未归一: %q", saved.Title)
	}
	if saved.Blocks[0].Content != "<script>keep</script>" {
		t.Fatalf("代码块内容不应做行内清洗: %q", saved.Blocks[0].Content)
	}
	if saved.Blocks[0].Props.Language != "go" {
		t.Fatalf("代码语言丢失: %q", saved.Blocks[0].Props.Language)
	}
	if saved.Blocks[1].Props.URL != "" {
		t.Fatalf("非法的图片地址应被清空: %q", saved.Blocks[1].Props.URL)
	}
	if len(saved.Blocks[2].Props.Rows) != 1 || !saved.Blocks[2].Props.HasHeader {
		t.Fatalf("表格数据异常: %+v", saved.Blocks[2].Props)
	}

	status, data = doJSON(t, app, http.MethodPut, "/api/pages/missing0000", map[string]any{"title": "x"})
	if status != http.StatusNotFound {
		t.Fatalf("不存在的页面应返回 404，实际 %d %s", status, data)
	}
}

func TestFavoriteAndOrder(t *testing.T) {
	_, app := newTestAPI(t)
	a := createPage(t, app, map[string]any{"title": "A"})
	b := createPage(t, app, map[string]any{"title": "B"})

	if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+a.ID+"/favorite", map[string]any{}); status != http.StatusOK {
		t.Fatalf("收藏失败: %d", status)
	}
	pages := workspace(t, app)
	if !findSummary(pages, a.ID).Favorite {
		t.Fatal("收藏状态未生效")
	}
	if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+a.ID+"/favorite", map[string]any{"value": false}); status != http.StatusOK {
		t.Fatalf("取消收藏失败: %d", status)
	}
	if findSummary(workspace(t, app), a.ID).Favorite {
		t.Fatal("取消收藏未生效")
	}
	if findSummary(pages, b.ID).Order <= findSummary(pages, a.ID).Order {
		t.Fatal("新建页面应排在后面")
	}
}

func TestMovePreventsCycles(t *testing.T) {
	_, app := newTestAPI(t)
	root := createPage(t, app, map[string]any{"title": "根"})
	child := createPage(t, app, map[string]any{"parentId": root.ID, "title": "子"})
	grand := createPage(t, app, map[string]any{"parentId": child.ID, "title": "孙"})

	status, data := doJSON(t, app, http.MethodPost, "/api/pages/"+root.ID+"/move", map[string]any{"parentId": grand.ID})
	if status != http.StatusBadRequest {
		t.Fatalf("移动到后代应被拒绝: %d %s", status, data)
	}
	status, data = doJSON(t, app, http.MethodPost, "/api/pages/"+root.ID+"/move", map[string]any{"parentId": root.ID})
	if status != http.StatusBadRequest {
		t.Fatalf("移动到自身应被拒绝: %d %s", status, data)
	}

	status, data = doJSON(t, app, http.MethodPost, "/api/pages/"+grand.ID+"/move", map[string]any{"parentId": "", "index": 0})
	if status != http.StatusOK {
		t.Fatalf("移动到根目录失败: %d %s", status, data)
	}
	pages := workspace(t, app)
	if got := findSummary(pages, grand.ID); got.ParentID != "" || got.Order != 0 {
		t.Fatalf("移动结果异常: %+v", got)
	}
	if got := findSummary(pages, child.ID); got.Order != 0 {
		t.Fatalf("旧层级应重新编号: %+v", got)
	}
}

func TestDuplicateCopiesSubtree(t *testing.T) {
	_, app := newTestAPI(t)
	root := createPage(t, app, map[string]any{"title": "项目"})
	child := createPage(t, app, map[string]any{"parentId": root.ID, "title": "子页面"})

	status, data := doJSON(t, app, http.MethodPost, "/api/pages/"+root.ID+"/duplicate", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("复制失败: %d %s", status, data)
	}
	var out struct {
		PageID string `json:"pageId"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !validID(out.PageID) || out.PageID == root.ID {
		t.Fatalf("副本 ID 异常: %q", out.PageID)
	}
	pages := workspace(t, app)
	copied := findSummary(pages, out.PageID)
	if copied == nil {
		t.Fatal("副本未写入工作区")
	}
	if copied.Title != "项目 (copy)" {
		t.Fatalf("副本标题异常: %q", copied.Title)
	}
	if copied.Order != findSummary(pages, root.ID).Order+1 {
		t.Fatalf("副本应紧随原页面之后: %d vs %d", copied.Order, findSummary(pages, root.ID).Order)
	}
	var copiedChild *PageSummary
	for i := range pages {
		if pages[i].ParentID == out.PageID {
			copiedChild = &pages[i]
		}
	}
	if copiedChild == nil {
		t.Fatal("子页面未被复制")
	}
	if copiedChild.ID == child.ID {
		t.Fatal("子页面应使用新 ID")
	}

	// 指定标题时直接使用。
	status, data = doJSON(t, app, http.MethodPost, "/api/pages/"+root.ID+"/duplicate", map[string]any{"title": "另一个项目"})
	if status != http.StatusOK {
		t.Fatalf("再次复制失败: %d %s", status, data)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if got := findSummary(workspace(t, app), out.PageID); got.Title != "另一个项目" {
		t.Fatalf("自定义副本标题未生效: %q", got.Title)
	}
}

func TestTrashRestoreCascade(t *testing.T) {
	_, app := newTestAPI(t)
	root := createPage(t, app, map[string]any{"title": "父"})
	child := createPage(t, app, map[string]any{"parentId": root.ID, "title": "子"})

	if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+root.ID+"/trash", nil); status != http.StatusOK {
		t.Fatalf("移入回收站失败: %d", status)
	}
	pages := workspace(t, app)
	if !findSummary(pages, root.ID).Trashed || !findSummary(pages, child.ID).Trashed {
		t.Fatal("回收站应级联标记子页面")
	}
	if findSummary(pages, child.ID).TrashedAt == "" {
		t.Fatal("回收站时间未记录")
	}

	// 父页面仍在回收站时恢复子页面：子页面回到根层级。
	if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+child.ID+"/restore", nil); status != http.StatusOK {
		t.Fatalf("恢复失败: %d", status)
	}
	pages = workspace(t, app)
	restored := findSummary(pages, child.ID)
	if restored.Trashed || restored.ParentID != "" {
		t.Fatalf("父页面已删除时应回到根层级: %+v", restored)
	}

	// 恢复父页面。
	if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+root.ID+"/restore", nil); status != http.StatusOK {
		t.Fatalf("恢复父页面失败: %d", status)
	}
	if findSummary(workspace(t, app), root.ID).Trashed {
		t.Fatal("父页面未恢复")
	}
}

func TestDeleteAndEmptyTrash(t *testing.T) {
	_, app := newTestAPI(t)
	keep := createPage(t, app, map[string]any{"title": "保留"})
	gone := createPage(t, app, map[string]any{"title": "删除"})
	goneChild := createPage(t, app, map[string]any{"parentId": gone.ID, "title": "删除的子页面"})

	if status, _ := doJSON(t, app, http.MethodDelete, "/api/pages/"+gone.ID, nil); status != http.StatusOK {
		t.Fatalf("删除失败: %d", status)
	}
	pages := workspace(t, app)
	if findSummary(pages, gone.ID) != nil || findSummary(pages, goneChild.ID) != nil {
		t.Fatal("删除应移除整棵子树")
	}
	if findSummary(pages, keep.ID) == nil {
		t.Fatal("其他页面不应受影响")
	}

	a := createPage(t, app, map[string]any{"title": "回收站 A"})
	b := createPage(t, app, map[string]any{"title": "回收站 B"})
	for _, page := range []Page{a, b} {
		if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+page.ID+"/trash", nil); status != http.StatusOK {
			t.Fatalf("移入回收站失败: %d", status)
		}
	}
	if status, _ := doJSON(t, app, http.MethodDelete, "/api/trash", nil); status != http.StatusOK {
		t.Fatalf("清空回收站失败: %d", status)
	}
	pages = workspace(t, app)
	for _, page := range []Page{a, b} {
		if findSummary(pages, page.ID) != nil {
			t.Fatalf("回收站清空后页面仍存在: %s", page.ID)
		}
	}
	if findSummary(pages, keep.ID) == nil {
		t.Fatal("清空回收站不应影响正常页面")
	}
}

func TestSearchMatchesTitleAndContent(t *testing.T) {
	_, app := newTestAPI(t)
	plain := createPage(t, app, map[string]any{"title": "普通页面"})
	trashed := createPage(t, app, map[string]any{"title": "路线图 Roadmap"})

	// 块内容命中。
	textPage := createPage(t, app, map[string]any{
		"title":  "季度计划",
		"blocks": []map[string]any{{"id": "cccc0001", "type": "paragraph", "content": "本季度目标是提升留存率"}},
	})
	if status, _ := doJSON(t, app, http.MethodPost, "/api/pages/"+trashed.ID+"/trash", nil); status != http.StatusOK {
		t.Fatalf("移入回收站失败: %d", status)
	}

	var results []SearchResult
	status, data := doJSON(t, app, http.MethodGet, "/api/search?q=%E7%95%99%E5%AD%98%E7%8E%87", nil)
	if status != http.StatusOK {
		t.Fatalf("搜索失败: %d %s", status, data)
	}
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != textPage.ID || results[0].Match != "content" {
		t.Fatalf("内容搜索结果异常: %+v", results)
	}
	if results[0].Snippet == "" {
		t.Fatal("搜索结果应包含摘要")
	}

	status, data = doJSON(t, app, http.MethodGet, "/api/search?q=roadmap", nil)
	if status != http.StatusOK {
		t.Fatalf("搜索失败: %d", status)
	}
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("回收站页面不应出现在搜索中: %+v", results)
	}

	status, data = doJSON(t, app, http.MethodGet, "/api/search?q=%E6%99%AE%E9%80%9A", nil)
	if status != http.StatusOK {
		t.Fatalf("搜索失败: %d", status)
	}
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != plain.ID || results[0].Match != "title" {
		t.Fatalf("标题搜索结果异常: %+v", results)
	}

	status, data = doJSON(t, app, http.MethodGet, "/api/search?q=", nil)
	if status != http.StatusOK {
		t.Fatalf("空搜索失败: %d", status)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("空查询应返回空数组: %s", data)
	}
}

func TestExportMarkdown(t *testing.T) {
	_, app := newTestAPI(t)
	page := createPage(t, app, map[string]any{
		"title": "导出测试",
		"blocks": []map[string]any{
			{"id": "dddd0001", "type": "heading2", "content": "小节"},
			{"id": "dddd0002", "type": "todo", "content": "已完成", "props": map[string]any{"checked": true}},
			{"id": "dddd0003", "type": "code", "content": "fmt.Println(1)", "props": map[string]any{"language": "go"}},
			{"id": "dddd0004", "type": "table", "props": map[string]any{"rows": [][]string{{"名称", "值"}, {"a", "1"}}, "hasHeader": true}},
			{"id": "dddd0005", "type": "image", "props": map[string]any{"url": "https://example.com/a.png", "caption": "示意图"}},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/pages/"+page.ID+"/export", nil)
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("导出失败: %d %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type 异常: %q", ct)
	}
	disposition := resp.Header.Get("Content-Disposition")
	if !strings.Contains(disposition, "filename*=UTF-8''") {
		t.Fatalf("应使用 UTF-8 文件名: %q", disposition)
	}
	text := string(body)
	for _, want := range []string{"# 导出测试", "## 小节", "- [x] 已完成", "```go", "| 名称 | 值 |", "--- |", "![示意图](https://example.com/a.png)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("导出的 Markdown 缺少 %q:\n%s", want, text)
		}
	}

	status, _ := doJSON(t, app, http.MethodGet, "/api/pages/missing0000/export", nil)
	if status != http.StatusNotFound {
		t.Fatalf("不存在的页面导出应返回 404，实际 %d", status)
	}
}

func doUpload(t *testing.T, app *fiber.App, filename string, content []byte) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestUploadValidationAndServing(t *testing.T) {
	_, app := newTestAPI(t)

	if status, _ := doUpload(t, app, "note.txt", []byte("hello")); status != http.StatusBadRequest {
		t.Fatalf("非图片扩展名应被拒绝，实际 %d", status)
	}
	if status, _ := doUpload(t, app, "fake.png", []byte("not an image")); status != http.StatusBadRequest {
		t.Fatalf("伪造扩展名应被拒绝，实际 %d", status)
	}

	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, bytes.Repeat([]byte{0}, 64)...)
	status, data := doUpload(t, app, "pic.png", png)
	if status != http.StatusOK {
		t.Fatalf("上传失败: %d %s", status, data)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(out.URL, "/api/apps/notes/files/")
	if !fileNamePattern.MatchString(name) || name == out.URL {
		t.Fatalf("上传地址异常: %q", out.URL)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/files/"+name, nil)
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("读取上传文件失败: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("应设置 nosniff")
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("静态图片应长缓存: %q", resp.Header.Get("Cache-Control"))
	}

	bad := httptest.NewRequest(http.MethodGet, "/api/files/../../notes.key", nil)
	badResp, err := app.Test(bad, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer badResp.Body.Close()
	if badResp.StatusCode == http.StatusOK {
		t.Fatal("非法文件名不应被读取")
	}
}

func TestDeleteRemovesUploadedFile(t *testing.T) {
	api, app := newTestAPI(t)
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	part, _ := w.CreateFormFile("file", "pic.png")
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, bytes.Repeat([]byte{0}, 64)...)
	_, _ = part.Write(png)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("上传响应: %v %s", err, data)
	}
	name := strings.TrimPrefix(out.URL, "/api/apps/notes/files/")
	if _, err := os.Stat(filepath.Join(api.filesDir, name)); err != nil {
		t.Fatalf("上传文件不存在: %v", err)
	}

	page := createPage(t, app, map[string]any{
		"title":  "带图片",
		"blocks": []map[string]any{{"id": "eeee0001", "type": "image", "props": map[string]any{"url": out.URL}}},
	})
	if status, _ := doJSON(t, app, http.MethodDelete, "/api/pages/"+page.ID, nil); status != http.StatusOK {
		t.Fatalf("删除页面失败: %d", status)
	}
	if _, err := os.Stat(filepath.Join(api.filesDir, name)); !os.IsNotExist(err) {
		t.Fatalf("删除页面应清理上传文件: %v", err)
	}
}

func TestNormalizeWorkspaceRepairsTree(t *testing.T) {
	api := &API{ws: Workspace{Pages: []Page{
		{ID: "aaaa1111", ParentID: "bbbb2222", Blocks: []Block{{ID: "x", Type: "unknown"}}},
		{ID: "bbbb2222", ParentID: "aaaa1111"},
		{ID: "cccc3333", ParentID: "missing000"},
		{ID: "dddd4444", ParentID: "dddd4444"},
		{ID: "dddd4444", ParentID: ""},
	}}}
	api.normalizeWorkspace()

	if len(api.ws.Pages) != 5 {
		t.Fatalf("重复 ID 应被重新分配而不是丢弃: %d", len(api.ws.Pages))
	}
	seen := map[string]bool{}
	for _, page := range api.ws.Pages {
		if seen[page.ID] {
			t.Fatalf("归一后仍有重复 ID: %s", page.ID)
		}
		seen[page.ID] = true
	}
	if api.findPage("cccc3333").ParentID != "" {
		t.Fatal("父页面缺失时应回根层级")
	}
	if api.findPage("dddd4444").ParentID != "" {
		t.Fatal("自引用页面应回根层级")
	}
	roots := 0
	for _, page := range api.ws.Pages {
		if page.ParentID == "" {
			roots++
		}
	}
	if roots != 4 {
		t.Fatalf("环应被打断，实际根页面数量: %d", roots)
	}
	if api.findPage("aaaa1111").Blocks[0].Type != "paragraph" {
		t.Fatal("非法块类型应被归一")
	}
}

func TestEquationAndDiagramBlocks(t *testing.T) {
	_, app := newTestAPI(t)
	page := createPage(t, app, map[string]any{
		"title": "公式与流程图",
		"blocks": []map[string]any{
			{"id": "ffff0001", "type": "equation", "content": `\frac{a}{b} <b>原样保留</b>`},
			{"id": "ffff0002", "type": "diagram", "content": "graph TD; A[开始] --> B{判断}"},
		},
	})
	if len(page.Blocks) != 2 {
		t.Fatalf("块数量错误: %d", len(page.Blocks))
	}
	// 公式与流程图正文是源码，不能按行内 HTML 清洗。
	if !strings.Contains(page.Blocks[0].Content, "<b>原样保留</b>") {
		t.Fatalf("公式源码被清洗: %q", page.Blocks[0].Content)
	}
	if page.Blocks[1].Content != "graph TD; A[开始] --> B{判断}" {
		t.Fatalf("流程图源码异常: %q", page.Blocks[1].Content)
	}

	// 保存时同样保留源码。
	status, data := doJSON(t, app, http.MethodPut, "/api/pages/"+page.ID, map[string]any{
		"title":  page.Title,
		"blocks": []map[string]any{{"id": "ffff0003", "type": "equation", "content": "a\nb"}},
	})
	if status != http.StatusOK {
		t.Fatalf("保存失败: %d %s", status, data)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pages/"+page.ID+"/export", nil)
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if !strings.Contains(text, "$$\na\nb\n$$") {
		t.Fatalf("多行公式应使用块级 $$ 包裹:\n%s", text)
	}
}

func TestInlineHelpers(t *testing.T) {
	if got := sanitizeInline(`<a href="javascript:alert(1)">x</a>`); strings.Contains(got, "javascript") {
		t.Fatalf("危险链接未清洗: %s", got)
	}
	if got := sanitizeInline(`<a href="https://example.com?a=1&b=2">链接</a>`); !strings.Contains(got, "&amp;") {
		t.Fatalf("链接属性应转义: %s", got)
	}
	if got := plainText("<b>你好</b><br>世界"); got != "你好 世界" {
		t.Fatalf("纯文本提取异常: %q", got)
	}
	if got := inlineToMarkdown(`<b>粗</b><a href="https://e.com">链</a><br>尾`); got != "**粗**[链](https://e.com)  \n尾" {
		t.Fatalf("Markdown 转换异常: %q", got)
	}
	if got := truncateRunes("一二三四", 2); got != "一二" {
		t.Fatalf("截断异常: %q", got)
	}
	if got := safeFilename(`a/b:c*d?e"f<g>h|i`); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Fatalf("文件名未净化: %q", got)
	}
	if got := makeSnippet("前文内容关键字后文", "关键字"); !strings.Contains(got, "关键字") {
		t.Fatalf("摘要应包含关键字: %q", got)
	}
}
