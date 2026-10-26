// Package apitool 提供接口调试与 Mock 服务应用。
package apitool

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
	"devos/internal/secure"
)

//go:embed web
var webFS embed.FS

const (
	maxSendBytes    = 8 << 20
	maxHistoryItems = 100
	maxMockLogs     = 100
)

// KeyValue 是请求参数、请求头、表单字段等通用的键值对。
type KeyValue struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// Auth 描述请求鉴权方式。
type Auth struct {
	Type     string `json:"type"` // none | bearer | basic | apikey
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
	Key      string `json:"key"`
	Value    string `json:"value"`
	In       string `json:"in"` // header | query
}

// RequestBody 描述请求体配置。
type RequestBody struct {
	Mode    string     `json:"mode"`    // none | raw | urlencoded | formdata
	Raw     string     `json:"raw"`     // 原始内容
	RawType string     `json:"rawType"` // json | text | xml | html
	Fields  []KeyValue `json:"fields"`  // 表单字段
}

// SavedRequest 是集合中的一条请求。
type SavedRequest struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Method          string      `json:"method"`
	URL             string      `json:"url"`
	Params          []KeyValue  `json:"params"`
	Headers         []KeyValue  `json:"headers"`
	Auth            Auth        `json:"auth"`
	Body            RequestBody `json:"body"`
	TimeoutMs       int         `json:"timeoutMs"`
	FollowRedirects *bool       `json:"followRedirects"`
	UpdatedAt       string      `json:"updatedAt"`
}

// Collection 是请求集合。
type Collection struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Requests []SavedRequest `json:"requests"`
}

// Environment 是环境变量组。
type Environment struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Variables []KeyValue `json:"variables"`
}

// HistoryItem 记录一次请求历史。
type HistoryItem struct {
	ID         string `json:"id"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Size       int    `json:"size"`
	At         string `json:"at"`
}

// MockSettings 保存 Mock 服务的全局开关与分组开关。
type MockSettings struct {
	Enabled bool        `json:"enabled"`
	Groups  []MockGroup `json:"groups"`
}

// MockGroup 是 Mock 规则分组，停用后组内规则不再参与匹配。
type MockGroup struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Workspace 是接口工具的全部持久化数据。
type Workspace struct {
	Collections       []Collection  `json:"collections"`
	Environments      []Environment `json:"environments"`
	ActiveEnvironment string        `json:"activeEnvironment"`
	History           []HistoryItem `json:"history"`
	Mock              MockSettings  `json:"mock"`
}

// MockRule 是一条 Mock 规则。
type MockRule struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Group   string            `json:"group"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Status  int               `json:"status"`
	DelayMs int               `json:"delayMs"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
	Body    string            `json:"body"`
	Enabled bool              `json:"enabled"`
}

// MockHit 是 Mock 服务的一次命中记录。
type MockHit struct {
	ID       string `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Query    string `json:"query"`
	Status   int    `json:"status"`
	Matched  bool   `json:"matched"`
	RuleName string `json:"ruleName"`
	At       string `json:"at"`
}

type API struct {
	mu    sync.RWMutex
	rules []MockRule
	ws    Workspace

	mockMu sync.RWMutex
	hits   []MockHit

	file   string
	wsFile string
	keys   *secure.Keyring
	log    *slog.Logger
}

func New() *API { return &API{} }

func (p *API) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "api",
		Name:        "接口工具",
		Description: "调试 HTTP 接口，并快速搭建 Mock 服务",
		Version:     "0.2.0",
		Category:    "开发者",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M8 4H6a2 2 0 0 0-2 2v4a2 2 0 0 1-2 2 2 2 0 0 1 2 2v4a2 2 0 0 0 2 2h2"/><path d="M16 4h2a2 2 0 0 1 2 2v4a2 2 0 0 0 2 2 2 2 0 0 0-2 2v4a2 2 0 0 1-2 2h-2"/></svg>`,
		Accent:      "#f59e0b",
		Entry:       "index.html",
		Width:       1180,
		Height:      740,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *API) Init(ctx *plugin.Context) error {
	p.file = filepath.Join(ctx.DataDir, "mocks.json")
	p.wsFile = filepath.Join(ctx.DataDir, "workspace.json")
	p.log = ctx.Logger
	keys, err := secure.Open(ctx.DataDir, "mocks.key")
	if err != nil {
		return fmt.Errorf("初始化加密密钥失败: %w", err)
	}
	p.keys = keys

	wsExists := fileExists(p.wsFile)
	if err := p.readEncrypted(p.file, &p.rules, "接口规则"); err != nil {
		return err
	}
	if err := p.readEncrypted(p.wsFile, &p.ws, "接口工作区"); err != nil {
		return err
	}
	p.normalizeWorkspace()
	if !wsExists {
		// 首次使用时开启 Mock 服务并写入示例数据，便于快速上手。
		p.ws.Mock.Enabled = true
		p.seedWorkspace(ctx.BaseURL)
	}
	if len(p.rules) == 0 {
		p.rules = []MockRule{{
			ID:      newID(),
			Name:    "示例接口",
			Method:  "GET",
			Path:    "/hello",
			Status:  200,
			Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"},
			Body:    `{"message":"Hello from DevOS Mock","ok":true}`,
			Enabled: true,
		}}
	}
	if err := p.persistMocks(); err != nil {
		return err
	}
	return p.persistWorkspace()
}

func (p *API) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *API) Register(api fiber.Router) {
	api.Post("/send", p.handleSend)
	api.Get("/workspace", p.handleWorkspaceGet)
	api.Post("/workspace", p.handleWorkspaceSave)

	api.Get("/mocks", func(c *fiber.Ctx) error {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return c.JSON(p.rules)
	})
	api.Post("/mocks", p.handleMocksSave)
	api.Delete("/mocks/:id", func(c *fiber.Ctx) error {
		id := c.Params("id")
		p.mu.Lock()
		out := p.rules[:0]
		for _, r := range p.rules {
			if r.ID != id {
				out = append(out, r)
			}
		}
		p.rules = out
		p.mu.Unlock()
		if err := p.persistMocks(); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})
	api.Get("/mock/logs", func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store")
		return c.JSON(p.mockLogList())
	})
	api.Delete("/mock/logs", func(c *fiber.Ctx) error {
		p.mockMu.Lock()
		p.hits = nil
		p.mockMu.Unlock()
		return c.JSON(fiber.Map{"ok": true})
	})
}

func (p *API) handleMocksSave(c *fiber.Ctx) error {
	var rules []MockRule
	if err := c.BodyParser(&rules); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	if len(rules) > 500 {
		return fiber.NewError(fiber.StatusBadRequest, "Mock 规则数量过多")
	}
	for i := range rules {
		normalizeRule(&rules[i])
	}
	p.mu.Lock()
	p.rules = rules
	p.mu.Unlock()
	if err := p.persistMocks(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(rules)
}

func (p *API) handleWorkspaceGet(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	p.mu.RLock()
	defer p.mu.RUnlock()
	return c.JSON(p.ws)
}

func (p *API) handleWorkspaceSave(c *fiber.Ctx) error {
	var in Workspace
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	p.mu.Lock()
	p.ws = in
	p.normalizeWorkspace()
	p.mu.Unlock()
	if err := p.persistWorkspace(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return c.JSON(p.ws)
}

// seedWorkspace 在首次启动时写入示例集合与环境变量，便于直接体验。
func (p *API) seedWorkspace(baseURL string) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "http://127.0.0.1:7788"
	}
	p.ws.Collections = []Collection{{
		ID:   newID(),
		Name: "示例集合",
		Requests: []SavedRequest{{
			ID:              newID(),
			Name:            "Hello Mock",
			Method:          http.MethodGet,
			URL:             "{{baseUrl}}/mock/hello",
			Params:          []KeyValue{},
			Headers:         []KeyValue{},
			Auth:            Auth{Type: "none"},
			Body:            RequestBody{Mode: "none", Fields: []KeyValue{}},
			TimeoutMs:       30000,
			FollowRedirects: boolPtr(true),
			UpdatedAt:       time.Now().Format(time.RFC3339),
		}},
	}}
	p.ws.Environments = []Environment{{
		ID:   newID(),
		Name: "本地",
		Variables: []KeyValue{
			{Key: "baseUrl", Value: base, Enabled: true},
		},
	}}
	p.ws.ActiveEnvironment = p.ws.Environments[0].ID
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func boolPtr(v bool) *bool { return &v }

func (p *API) normalizeWorkspace() {
	if p.ws.Collections == nil {
		p.ws.Collections = []Collection{}
	}
	if p.ws.Environments == nil {
		p.ws.Environments = []Environment{}
	}
	if p.ws.History == nil {
		p.ws.History = []HistoryItem{}
	}
	if len(p.ws.Collections) > 200 {
		p.ws.Collections = p.ws.Collections[:200]
	}
	for i := range p.ws.Collections {
		c := &p.ws.Collections[i]
		if c.ID == "" {
			c.ID = newID()
		}
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = "未命名集合"
		}
		if len(c.Requests) > 500 {
			c.Requests = c.Requests[:500]
		}
		for j := range c.Requests {
			normalizeRequest(&c.Requests[j])
		}
	}
	if len(p.ws.Environments) > 50 {
		p.ws.Environments = p.ws.Environments[:50]
	}
	for i := range p.ws.Environments {
		env := &p.ws.Environments[i]
		if env.ID == "" {
			env.ID = newID()
		}
		env.Name = strings.TrimSpace(env.Name)
		if env.Name == "" {
			env.Name = "未命名环境"
		}
	}
	if len(p.ws.History) > maxHistoryItems {
		p.ws.History = p.ws.History[:maxHistoryItems]
	}
	// 活动环境失效时回退为空。
	if p.ws.ActiveEnvironment != "" {
		found := false
		for _, env := range p.ws.Environments {
			if env.ID == p.ws.ActiveEnvironment {
				found = true
				break
			}
		}
		if !found {
			p.ws.ActiveEnvironment = ""
		}
	}
}

func normalizeRequest(req *SavedRequest) {
	if req.ID == "" {
		req.ID = newID()
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	req.Method = strings.ToUpper(req.Method)
	if req.Params == nil {
		req.Params = []KeyValue{}
	}
	if req.Headers == nil {
		req.Headers = []KeyValue{}
	}
	if req.Body.Fields == nil {
		req.Body.Fields = []KeyValue{}
	}
	if req.Body.Mode == "" {
		req.Body.Mode = "none"
	}
	if req.Auth.Type == "" {
		req.Auth.Type = "none"
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = 30000
	}
	if req.TimeoutMs > 120000 {
		req.TimeoutMs = 120000
	}
	if req.FollowRedirects == nil {
		follow := true
		req.FollowRedirects = &follow
	}
}

func normalizeRule(rule *MockRule) {
	if rule.ID == "" {
		rule.ID = newID()
	}
	if rule.Method == "" {
		rule.Method = http.MethodGet
	}
	rule.Method = strings.ToUpper(rule.Method)
	if rule.Path == "" {
		rule.Path = "/"
	}
	if !strings.HasPrefix(rule.Path, "/") {
		rule.Path = "/" + rule.Path
	}
	if rule.Status < 100 || rule.Status > 599 {
		rule.Status = 200
	}
	if rule.DelayMs < 0 {
		rule.DelayMs = 0
	}
	if rule.DelayMs > 10000 {
		rule.DelayMs = 10000
	}
	if rule.Headers == nil {
		rule.Headers = map[string]string{}
	}
	if rule.Query == nil {
		rule.Query = map[string]string{}
	}
}

// RegisterPublic 在 /mock 下挂载对外的 Mock 服务。
func (p *API) RegisterPublic(router fiber.Router) {
	router.All("/*", p.serveMock)
	router.All("/", p.serveMock)
}

func (p *API) serveMock(c *fiber.Ctx) error {
	c.Set("Access-Control-Allow-Origin", "*")
	c.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	c.Set("Access-Control-Allow-Headers", "*")
	if c.Method() == fiber.MethodOptions {
		return c.SendStatus(fiber.StatusNoContent)
	}

	p.mu.RLock()
	enabled := p.ws.Mock.Enabled
	rules := make([]MockRule, len(p.rules))
	copy(rules, p.rules)
	p.mu.RUnlock()

	path := c.Path()
	// 挂载在 /mock 分组下时，Fiber 的 Path 仍带分组前缀，这里只剥离一次。
	if strings.HasPrefix(path, "/mock") {
		path = strings.TrimPrefix(path, "/mock")
	}
	if path == "" {
		path = "/"
	}
	query := c.Context().QueryArgs()
	queryMap := map[string]string{}
	queryArgs := url.Values{}
	query.VisitAll(func(k, v []byte) {
		queryArgs.Add(string(k), string(v))
		queryMap[string(k)] = string(v)
	})

	if !enabled {
		p.recordMockHit(c.Method(), path, string(c.Request().URI().QueryString()), fiber.StatusServiceUnavailable, false, "")
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "Mock 服务已关闭",
			"path":  path,
		})
	}

	var hit *MockRule
	var vars map[string]string
	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled {
			continue
		}
		if rule.Method != "ANY" && !strings.EqualFold(rule.Method, c.Method()) {
			continue
		}
		pathVars, ok := matchMockPath(rule.Path, path)
		if !ok || !matchMockQuery(rule.Query, queryArgs) {
			continue
		}
		hit = rule
		vars = pathVars
		break
	}

	if hit == nil {
		p.recordMockHit(c.Method(), path, string(c.Request().URI().QueryString()), fiber.StatusNotFound, false, "")
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "mock 规则未命中",
			"path":  path,
			"mock":  "/mock" + path,
		})
	}

	for key, value := range queryMap {
		if _, exists := vars[key]; !exists {
			vars[key] = value
		}
	}

	if hit.DelayMs > 0 {
		time.Sleep(time.Duration(hit.DelayMs) * time.Millisecond)
	}
	for k, v := range hit.Headers {
		c.Set(k, renderMockTemplate(v, vars))
	}
	if _, ok := hit.Headers["Content-Type"]; !ok {
		c.Set("Content-Type", "application/json; charset=utf-8")
	}
	p.recordMockHit(c.Method(), path, string(c.Request().URI().QueryString()), hit.Status, true, hit.Name)
	return c.Status(hit.Status).SendString(renderMockTemplate(hit.Body, vars))
}

func (p *API) recordMockHit(method, path, query string, status int, matched bool, ruleName string) {
	p.mockMu.Lock()
	defer p.mockMu.Unlock()
	// Fiber 的 Path/QueryString 可能引用可复用的请求缓冲区，必须复制后再保存。
	p.hits = append([]MockHit{{
		ID:       newID(),
		Method:   strings.ToUpper(method),
		Path:     strings.Clone(path),
		Query:    strings.Clone(query),
		Status:   status,
		Matched:  matched,
		RuleName: ruleName,
		At:       time.Now().Format(time.RFC3339),
	}}, p.hits...)
	if len(p.hits) > maxMockLogs {
		p.hits = p.hits[:maxMockLogs]
	}
}

func (p *API) mockLogList() []MockHit {
	p.mockMu.RLock()
	defer p.mockMu.RUnlock()
	out := make([]MockHit, len(p.hits))
	copy(out, p.hits)
	return out
}

// matchMockPath 支持精确路径、:param 路径参数与 * 通配段。
func matchMockPath(rulePath, reqPath string) (map[string]string, bool) {
	ruleSegs := splitPath(rulePath)
	reqSegs := splitPath(reqPath)
	vars := map[string]string{}
	for i, seg := range ruleSegs {
		if seg == "*" {
			// 通配段匹配剩余所有路径（可为空）。
			return vars, true
		}
		if i >= len(reqSegs) {
			return nil, false
		}
		if strings.HasPrefix(seg, ":") && len(seg) > 1 {
			vars[seg[1:]] = reqSegs[i]
			continue
		}
		if seg != reqSegs[i] {
			return nil, false
		}
	}
	if len(reqSegs) != len(ruleSegs) {
		return nil, false
	}
	return vars, true
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// matchMockQuery 要求规则中声明的查询参数全部匹配；值为空时只要求参数存在。
func matchMockQuery(ruleQuery map[string]string, actual url.Values) bool {
	for key, want := range ruleQuery {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !actual.Has(key) {
			return false
		}
		if want != "" && actual.Get(key) != want {
			return false
		}
	}
	return true
}

// renderMockTemplate 把 {{变量}} 替换为路径参数或查询参数的值。
func renderMockTemplate(text string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(text, "{{") {
		return text
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		text = strings.ReplaceAll(text, "{{"+key+"}}", vars[key])
	}
	return text
}

type sendInput struct {
	Method          string      `json:"method"`
	URL             string      `json:"url"`
	Params          []KeyValue  `json:"params"`
	Headers         []KeyValue  `json:"headers"`
	Auth            Auth        `json:"auth"`
	Body            RequestBody `json:"body"`
	TimeoutMs       int         `json:"timeoutMs"`
	FollowRedirects *bool       `json:"followRedirects"`
}

type responseCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  string `json:"expires"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
}

type headerPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (p *API) handleSend(c *fiber.Ctx) error {
	var in sendInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	rawURL := strings.TrimSpace(in.URL)
	if rawURL == "" {
		return fiber.NewError(fiber.StatusBadRequest, "请输入请求地址")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fiber.NewError(fiber.StatusBadRequest, "请输入合法的 http/https 地址")
	}

	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}

	query := u.Query()
	for _, item := range in.Params {
		if !item.Enabled || strings.TrimSpace(item.Key) == "" {
			continue
		}
		query.Add(item.Key, item.Value)
	}
	if in.Auth.Type == "apikey" && strings.EqualFold(in.Auth.In, "query") && strings.TrimSpace(in.Auth.Key) != "" {
		query.Set(in.Auth.Key, in.Auth.Value)
	}
	u.RawQuery = query.Encode()

	body, contentType, err := buildRequestBody(in.Body)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	for _, item := range in.Headers {
		if !item.Enabled || strings.TrimSpace(item.Key) == "" {
			continue
		}
		req.Header.Set(strings.TrimSpace(item.Key), item.Value)
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	applyAuth(req, in.Auth)

	timeout := 30 * time.Second
	if in.TimeoutMs > 0 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
		if timeout > 2*time.Minute {
			timeout = 2 * time.Minute
		}
	}
	follow := true
	if in.FollowRedirects != nil {
		follow = *in.FollowRedirects
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return errors.New("重定向次数过多")
			}
			return nil
		},
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSendBytes+1))
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	truncated := false
	if len(data) > maxSendBytes {
		data = data[:maxSendBytes]
		truncated = true
	}

	headers := make(map[string]string, len(resp.Header))
	var headerList []headerPair
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		values := resp.Header.Values(k)
		headers[k] = strings.Join(values, ", ")
		for _, v := range values {
			headerList = append(headerList, headerPair{Key: k, Value: v})
		}
	}

	cookies := make([]responseCookie, 0, len(resp.Cookies()))
	for _, ck := range resp.Cookies() {
		expires := ""
		if !ck.Expires.IsZero() {
			expires = ck.Expires.Format(time.RFC3339)
		}
		cookies = append(cookies, responseCookie{
			Name:     ck.Name,
			Value:    ck.Value,
			Domain:   ck.Domain,
			Path:     ck.Path,
			Expires:  expires,
			HTTPOnly: ck.HttpOnly,
			Secure:   ck.Secure,
		})
	}

	finalURL := u.String()
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return c.JSON(fiber.Map{
		"status":     resp.StatusCode,
		"statusText": resp.Status,
		"headers":    headers,
		"headerList": headerList,
		"cookies":    cookies,
		"body":       string(data),
		"size":       len(data),
		"truncated":  truncated,
		"durationMs": time.Since(start).Milliseconds(),
		"finalURL":   finalURL,
	})
}

func buildRequestBody(body RequestBody) (io.Reader, string, error) {
	switch body.Mode {
	case "", "none":
		return nil, "", nil
	case "raw":
		if body.Raw == "" {
			return nil, "", nil
		}
		contentType := "text/plain; charset=utf-8"
		switch body.RawType {
		case "json":
			contentType = "application/json; charset=utf-8"
		case "xml":
			contentType = "application/xml; charset=utf-8"
		case "html":
			contentType = "text/html; charset=utf-8"
		}
		return strings.NewReader(body.Raw), contentType, nil
	case "urlencoded":
		values := url.Values{}
		for _, field := range body.Fields {
			if !field.Enabled || strings.TrimSpace(field.Key) == "" {
				continue
			}
			values.Add(field.Key, field.Value)
		}
		return strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil
	case "formdata":
		buf := &bytes.Buffer{}
		writer := multipart.NewWriter(buf)
		for _, field := range body.Fields {
			if !field.Enabled || strings.TrimSpace(field.Key) == "" {
				continue
			}
			if err := writer.WriteField(field.Key, field.Value); err != nil {
				return nil, "", err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", err
		}
		return buf, writer.FormDataContentType(), nil
	default:
		return nil, "", fmt.Errorf("不支持的请求体类型: %s", body.Mode)
	}
}

func applyAuth(req *http.Request, auth Auth) {
	switch auth.Type {
	case "bearer":
		if token := strings.TrimSpace(auth.Token); token != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "basic":
		if req.Header.Get("Authorization") != "" {
			return
		}
		value := base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password))
		req.Header.Set("Authorization", "Basic "+value)
	case "apikey":
		if strings.EqualFold(auth.In, "query") || strings.TrimSpace(auth.Key) == "" {
			return
		}
		req.Header.Set(auth.Key, auth.Value)
	}
}

func (p *API) readEncrypted(path string, target any, label string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	plain, err := p.keys.DecryptBytes(data)
	if err != nil {
		return fmt.Errorf("解密%s失败: %w", label, err)
	}
	if err := json.Unmarshal(plain, target); err != nil {
		return fmt.Errorf("解析%s失败: %w", label, err)
	}
	if string(plain) == string(data) {
		// 历史明文数据在加载时立即重新加密落盘。
		return p.writeEncrypted(path, target)
	}
	return nil
}

func (p *API) writeEncrypted(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encrypted, err := p.keys.EncryptBytes(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encrypted, '\n'), 0o600)
}

func (p *API) persistMocks() error {
	p.mu.RLock()
	rules := p.rules
	p.mu.RUnlock()
	return p.writeEncrypted(p.file, rules)
}

func (p *API) persistWorkspace() error {
	p.mu.RLock()
	ws := p.ws
	p.mu.RUnlock()
	return p.writeEncrypted(p.wsFile, ws)
}

func newID() string {
	buf := make([]byte, 5)
	_, _ = rand.Re