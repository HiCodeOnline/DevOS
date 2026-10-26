package apitool

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

func newTestAPI(t *testing.T) (*API, *fiber.App) {
	t.Helper()
	dir := t.TempDir()
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
	api.RegisterPublic(app.Group("/mock"))
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

func TestInitSeedsAndEncrypts(t *testing.T) {
	api, _ := newTestAPI(t)

	if !api.ws.Mock.Enabled {
		t.Fatal("首次启动应默认开启 Mock 服务")
	}
	if len(api.ws.Collections) != 1 || len(api.ws.Collections[0].Requests) != 1 {
		t.Fatalf("示例集合未写入: %+v", api.ws.Collections)
	}
	if len(api.ws.Environments) != 1 || api.ws.ActiveEnvironment != api.ws.Environments[0].ID {
		t.Fatalf("示例环境未写入: %+v", api.ws.Environments)
	}
	if len(api.rules) != 1 {
		t.Fatalf("示例 Mock 规则未写入: %d", len(api.rules))
	}

	for _, name := range []string{"mocks.json", "workspace.json"} {
		data, err := os.ReadFile(filepath.Join(api.file, "..", name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if !strings.HasPrefix(string(data), "enc:v1:") {
			t.Fatalf("%s 未加密落盘", name)
		}
	}
}

func TestMockMatchers(t *testing.T) {
	vars, ok := matchMockPath("/users/:id/posts/*", "/users/42/posts/9")
	if !ok {
		t.Fatal("通配路径应命中")
	}
	if vars["id"] != "42" {
		t.Fatalf("路径参数错误: %+v", vars)
	}

	if _, ok := matchMockPath("/users/:id", "/users"); ok {
		t.Fatal("段数不一致不应命中")
	}

	query, _ := url.ParseQuery("page=2&size=10")
	if !matchMockQuery(map[string]string{"page": "2", "size": ""}, query) {
		t.Fatal("查询参数应命中")
	}
	if matchMockQuery(map[string]string{"page": "3"}, query) {
		t.Fatal("查询参数值不一致不应命中")
	}
	if matchMockQuery(map[string]string{"missing": ""}, query) {
		t.Fatal("缺少声明参数不应命中")
	}

	got := renderMockTemplate(`{"id":"{{id}}","page":"{{page}}"}`,
		map[string]string{"id": "42", "page": "2"})
	if got != `{"id":"42","page":"2"}` {
		t.Fatalf("模板替换失败: %s", got)
	}
}

func TestServeMock(t *testing.T) {
	_, app := newTestAPI(t)

	rules := []MockRule{{
		ID:      "r1",
		Name:    "用户详情",
		Method:  "GET",
		Path:    "/users/:id",
		Status:  201,
		Headers: map[string]string{"X-Rule": "{{id}}"},
		Query:   map[string]string{"name": ""},
		Body:    `{"id":"{{id}}","name":"{{name}}"}`,
		Enabled: true,
	}}
	if status, _ := doJSON(t, app, http.MethodPost, "/api/mocks", rules); status != http.StatusOK {
		t.Fatalf("保存规则失败: %d", status)
	}

	req := httptest.NewRequest(http.MethodGet, "/mock/users/42?name=amy", nil)
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatalf("状态码错误: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Rule") != "42" {
		t.Fatalf("响应头模板未替换: %q", resp.Header.Get("X-Rule"))
	}
	if string(body) != `{"id":"42","name":"amy"}` {
		t.Fatalf("响应体模板未替换: %s", body)
	}

	miss := httptest.NewRequest(http.MethodGet, "/mock/unknown", nil)
	missResp, err := app.Test(miss, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer missResp.Body.Close()
	if missResp.StatusCode != http.StatusNotFound {
		t.Fatalf("未命中应返回 404，实际 %d", missResp.StatusCode)
	}

	// 全局关闭后一律 503。
	ws := apiWorkspace(t, app)
	ws.Mock.Enabled = false
	if status, _ := doJSON(t, app, http.MethodPost, "/api/workspace", ws); status != http.StatusOK {
		t.Fatalf("保存工作区失败: %d", status)
	}
	off := httptest.NewRequest(http.MethodGet, "/mock/users/42?name=amy", nil)
	offResp, err := app.Test(off, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer offResp.Body.Close()
	if offResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("关闭后应返回 503，实际 %d", offResp.StatusCode)
	}

	// OPTIONS 预检直接 204。
	opt := httptest.NewRequest(http.MethodOptions, "/mock/users/42", nil)
	optResp, err := app.Test(opt, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer optResp.Body.Close()
	if optResp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS 应返回 204，实际 %d", optResp.StatusCode)
	}
}

func apiWorkspace(t *testing.T, app *fiber.App) Workspace {
	t.Helper()
	status, data := doJSON(t, app, http.MethodGet, "/api/workspace", nil)
	if status != http.StatusOK {
		t.Fatalf("读取工作区失败: %d", status)
	}
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatalf("解析工作区: %v", err)
	}
	return ws
}

func TestSendRequest(t *testing.T) {
	var seen struct {
		method string
		path   string
		query  string
		auth   string
		kv     string
		ctype  string
		body   string
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.method = r.Method
		seen.path = r.URL.Path
		seen.query = r.URL.RawQuery
		seen.auth = r.Header.Get("Authorization")
		seen.kv = r.Header.Get("X-Demo")
		seen.ctype = r.Header.Get("Content-Type")
		seen.body = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "sid=abc; Path=/; HttpOnly")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	_, app := newTestAPI(t)
	payload := map[string]any{
		"method":  "post",
		"url":     target.URL + "/items?keep=1",
		"params":  []KeyValue{{Key: "page", Value: "2", Enabled: true}, {Key: "off", Value: "1"}},
		"headers": []KeyValue{{Key: "X-Demo", Value: "yes", Enabled: true}},
		"auth":    Auth{Type: "bearer", Token: "token-1"},
		"body":    RequestBody{Mode: "raw", Raw: `{"name":"devos"}`, RawType: "json"},
	}
	status, data := doJSON(t, app, http.MethodPost, "/api/send", payload)
	if status != http.StatusOK {
		t.Fatalf("发送失败: %d %s", status, data)
	}

	var out struct {
		Status     int              `json:"status"`
		Body       string           `json:"body"`
		Size       int              `json:"size"`
		FinalURL   string           `json:"finalURL"`
		Cookies    []responseCookie `json:"cookies"`
		HeaderList []headerPair     `json:"headerList"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != http.StatusOK || out.Body != `{"ok":true}` || out.FinalURL != target.URL+"/items?keep=1&page=2" {
		t.Fatalf("响应内容异常: %+v", out)
	}
	if len(out.Cookies) != 1 || out.Cookies[0].Name != "sid" || !out.Cookies[0].HTTPOnly {
		t.Fatalf("Cookie 解析异常: %+v", out.Cookies)
	}
	if len(out.HeaderList) == 0 {
		t.Fatal("响应头列表为空")
	}

	if seen.method != http.MethodPost || seen.path != "/items" {
		t.Fatalf("请求方法/路径错误: %+v", seen)
	}
	if seen.query != "keep=1&page=2" {
		t.Fatalf("查询参数合并错误: %s", seen.query)
	}
	if seen.auth != "Bearer token-1" || seen.kv != "yes" {
		t.Fatalf("请求头处理错误: %+v", seen)
	}
	if seen.ctype != "application/json; charset=utf-8" || seen.body != `{"name":"devos"}` {
		t.Fatalf("请求体处理错误: %+v", seen)
	}
}

func TestSendURLEncodedAndNoRedirect(t *testing.T) {
	var form url.Values
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_ = r.ParseForm()
		form = r.PostForm
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("done"))
	}))
	defer target.Close()

	_, app := newTestAPI(t)
	no := false
	payload := map[string]any{
		"method":          "PUT",
		"url":             target.URL + "/submit",
		"body":            RequestBody{Mode: "urlencoded", Fields: []KeyValue{{Key: "a", Value: "1", Enabled: true}, {Key: "skip", Value: "x"}}},
		"followRedirects": no,
	}
	status, data := doJSON(t, app, http.MethodPost, "/api/send", payload)
	if status != http.StatusOK {
		t.Fatalf("发送失败: %d %s", status, data)
	}
	var out struct {
		Status int `json:"status"`
	}
	_ = json.Unmarshal(data, &out)
	if out.Status != http.StatusCreated || form.Get("a") != "1" || form.Has("skip") {
		t.Fatalf("表单编码错误: status=%d form=%v", out.Status, form)
	}

	redirect := map[string]any{
		"method":          "GET",
		"url":             target.URL + "/redirect",
		"followRedirects": false,
	}
	status, data = doJSON(t, app, http.MethodPost, "/api/send", redirect)
	if status != http.StatusOK {
		t.Fatalf("发送失败: %d %s", status, data)
	}
	out.Status = 0
	_ = json.Unmarshal(data, &out)
	if out.Status != http.StatusFound {
		t.Fatalf("关闭重定向后应保留 302，实际 %d", out.Status)
	}
}

func TestWorkspacePersistAndReload(t *testing.T) {
	api, app := newTestAPI(t)
	ws := apiWorkspace(t, app)
	ws.Collections = append(ws.Collections, Collection{
		ID:   "c1",
		Name: "订单服务",
		Requests: []SavedRequest{{
			ID:     "q1",
			Name:   "创建订单",
			Method: "post",
			URL:    "{{baseUrl}}/orders",
			Auth:   Auth{Type: "bearer", Token: "t"},
			Body:   RequestBody{Mode: "raw", Raw: `{}`, RawType: "json"},
		}},
	})
	ws.History = []HistoryItem{{ID: "h1", Method: "POST", URL: "{{baseUrl}}/orders", Status: 201, At: "2026-01-01T00:00:00Z"}}
	if status, data := doJSON(t, app, http.MethodPost, "/api/workspace", ws); status != http.StatusOK {
		t.Fatalf("保存工作区失败: %d %s", status, data)
	}

	reloaded := New()
	ctx := &plugin.Context{DataDir: filepath.Dir(api.file), BaseURL: "http://127.0.0.1:7788", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := reloaded.Init(ctx); err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if len(reloaded.ws.Collections) != 2 {
		t.Fatalf("集合数量错误: %d", len(reloaded.ws.Collections))
	}
	last := reloaded.ws.Collections[1]
	if last.Name != "订单服务" || last.Requests[0].Method != "POST" || last.Requests[0].Auth.Token != "t" {
		t.Fatalf("集合内容错误: %+v", last)
	}
	if len(reloaded.ws.History) != 1 || reloaded.ws.History[0].ID != "h1" {
		t.Fatalf("历史记录错误: %+v", reloaded.ws.History)
	}
}

func TestMockLogs(t *testing.T) {
	_, app := newTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/mock/hello", nil)
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("示例规则应命中: %d", resp.StatusCode)
	}

	status, data := doJSON(t, app, http.MethodGet, "/api/mock/logs", nil)
	if status != http.StatusOK {
		t.Fatalf("读取日志失败: %d", status)
	}
	var logs []MockHit
	if err := json.Unmarshal(data, &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || !logs[0].Matched || logs[0].Path != "/hello" {
		t.Fatalf("日志内容错误: %+v", logs)
	}

	if status, _ := doJSON(t, app, http.MethodDelete, "/api/mock/logs", nil); status != http.StatusOK {
		t.Fatalf("清空日志失败: %d", status)
	}
	_, data = doJSON(t, app, http.MethodGet, "/api/mock/logs", nil)
	logs = nil
	_ = json.Unmarshal(data, &logs)
	if len(logs) != 0 {
		t.Fatalf("日志未清空: %+v", logs)
	}
}
