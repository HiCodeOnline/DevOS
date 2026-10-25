// Package apitool 提供接口调试与 Mock 服务应用。
package apitool

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
	"devos/internal/secure"
)

//go:embed web
var webFS embed.FS

// MockRule 是一条 Mock 规则。
type MockRule struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Status  int               `json:"status"`
	DelayMs int               `json:"delayMs"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Enabled bool              `json:"enabled"`
}

type API struct {
	mu    sync.RWMutex
	rules []MockRule
	file  string
	keys  *secure.Keyring
	log   *slog.Logger
}

func New() *API { return &API{} }

func (p *API) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "api",
		Name:        "接口工具",
		Description: "调试 HTTP 接口，并快速搭建 Mock 服务",
		Version:     "0.1.0",
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
	p.log = ctx.Logger
	keys, err := secure.Open(ctx.DataDir, "mocks.key")
	if err != nil {
		return fmt.Errorf("初始化加密密钥失败: %w", err)
	}
	p.keys = keys
	if err := p.load(); err != nil {
		return err
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
		return p.persist()
	}
	return nil
}

func (p *API) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *API) Register(api fiber.Router) {
	api.Post("/send", p.handleSend)
	api.Get("/mocks", func(c *fiber.Ctx) error {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return c.JSON(p.rules)
	})
	api.Post("/mocks", func(c *fiber.Ctx) error {
		var rules []MockRule
		if err := c.BodyParser(&rules); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		for i := range rules {
			if rules[i].ID == "" {
				rules[i].ID = newID()
			}
			if rules[i].Method == "" {
				rules[i].Method = "GET"
			}
			if rules[i].Path == "" {
				rules[i].Path = "/"
			}
			if rules[i].Status == 0 {
				rules[i].Status = 200
			}
		}
		p.mu.Lock()
		p.rules = rules
		p.mu.Unlock()
		if err := p.persist(); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(rules)
	})
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
		if err := p.persist(); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})
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

	path := strings.TrimPrefix(c.OriginalURL(), "/mock")
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		path = "/"
	}

	p.mu.RLock()
	var hit *MockRule
	for i := range p.rules {
		r := p.rules[i]
		if !r.Enabled {
			continue
		}
		if r.Method != "ANY" && !strings.EqualFold(r.Method, c.Method()) {
			continue
		}
		if matchPath(r.Path, path) {
			hit = &r
			break
		}
	}
	p.mu.RUnlock()

	if hit == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "mock 规则未命中",
			"path":  path,
			"mock":  "/mock" + path,
		})
	}
	if hit.DelayMs > 0 {
		delay := hit.DelayMs
		if delay > 10000 {
			delay = 10000
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	for k, v := range hit.Headers {
		c.Set(k, v)
	}
	if _, ok := hit.Headers["Content-Type"]; !ok {
		c.Set("Content-Type", "application/json; charset=utf-8")
	}
	return c.Status(hit.Status).SendString(hit.Body)
}

func matchPath(rulePath, reqPath string) bool {
	if strings.HasSuffix(rulePath, "*") {
		return strings.HasPrefix(reqPath, strings.TrimSuffix(rulePath, "*"))
	}
	return strings.TrimSuffix(rulePath, "/") == strings.TrimSuffix(reqPath, "/") || rulePath == reqPath
}

func (p *API) handleSend(c *fiber.Ctx) error {
	var in struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fiber.NewError(fiber.StatusBadRequest, "请输入合法的 http/https 地址")
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}

	var reader io.Reader
	if in.Body != "" && method != http.MethodGet && method != http.MethodHead {
		reader = strings.NewReader(in.Body)
	}
	req, err := http.NewRequest(method, in.URL, reader)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	for k, v := range in.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		headers[k] = strings.Join(v, ", ")
	}
	return c.JSON(fiber.Map{
		"status":     resp.StatusCode,
		"statusText": resp.Status,
		"headers":    headers,
		"body":       string(body),
		"size":       len(body),
		"durationMs": time.Since(start).Milliseconds(),
	})
}

func (p *API) load() error {
	data, err := os.ReadFile(p.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	plain, err := p.keys.DecryptBytes(data)
	if err != nil {
		return fmt.Errorf("解密接口规则失败: %w", err)
	}
	if err := json.Unmarshal(plain, &p.rules); err != nil {
		return err
	}
	if string(plain) == string(data) {
		// 历史明文数据在加载时立即重新加密落盘。
		return p.persist()
	}
	return nil
}

func (p *API) persist() error {
	p.mu.RLock()
	data, err := json.MarshalIndent(p.rules, "", "  ")
	p.mu.RUnlock()
	if err != nil {
		return err
	}
	encrypted, err := p.keys.EncryptBytes(data)
	if err != nil {
		return err
	}
	return os.WriteFile(p.file, append(encrypted, '\n'), 0o600)
}

func newID() string {
	buf := make([]byte, 5)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
