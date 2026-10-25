// Package sshapp 提供 SSH 会话管理应用（会话库 + 多标签终端）。
package sshapp

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"

	"devos/internal/plugin"
	"devos/internal/profile"
	"devos/internal/secure"
	"devos/internal/sshtools"
)

//go:embed web
var webFS embed.FS

type SSH struct {
	store   *profile.Store
	log     *slog.Logger
	dataDir string
}

func New() *SSH { return &SSH{} }

func (p *SSH) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "ssh",
		Name:        "SSH 终端",
		Description: "会话管理、多标签终端、跳板机与编码支持",
		Version:     "0.2.0",
		Category:    "开发者",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M7 9l3 3-3 3"/><path d="M13 15h4"/></svg>`,
		Accent:      "#0ea5e9",
		Entry:       "index.html",
		Width:       1180,
		Height:      740,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *SSH) Init(ctx *plugin.Context) error {
	store, err := profile.Open(ctx.RootDir)
	if err != nil {
		return err
	}
	p.store = store
	p.log = ctx.Logger
	p.dataDir = ctx.DataDir
	return nil
}

func (p *SSH) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *SSH) Register(api fiber.Router) {
	api.Get("/profiles", func(c *fiber.Ctx) error {
		return c.JSON(p.store.List())
	})

	api.Post("/profiles", func(c *fiber.Ctx) error {
		var in profile.Profile
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		if in.Host == "" || in.User == "" {
			return fiber.NewError(fiber.StatusBadRequest, "主机与用户名不能为空")
		}
		saved, err := p.store.Save(in)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(saved)
	})

	api.Post("/profiles/:id/duplicate", func(c *fiber.Ctx) error {
		cloned, err := p.store.Duplicate(c.Params("id"))
		if err != nil {
			return fiber.NewError(fiber.StatusNotFound, err.Error())
		}
		return c.JSON(cloned)
	})

	api.Delete("/profiles/:id", func(c *fiber.Ctx) error {
		if err := p.store.Delete(c.Params("id")); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	api.Post("/export", func(c *fiber.Ctx) error {
		payload, err := json.Marshal(p.store.List())
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		password, err := secure.NewExportPassword()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		content, err := secure.Seal(password, payload)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{
			"filename": "devos-sessions.json",
			"key":      password,
			"content":  string(content),
		})
	})

	api.Post("/import", func(c *fiber.Ctx) error {
		var in struct {
			Content string `json:"content"`
			Key     string `json:"key"`
		}
		if err := c.BodyParser(&in); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
		}
		raw := []byte(strings.TrimSpace(in.Content))
		if len(raw) == 0 {
			return fiber.NewError(fiber.StatusBadRequest, "导入文件内容为空")
		}
		payload := raw
		if secure.IsEnvelope(raw) {
			if strings.TrimSpace(in.Key) == "" {
				return fiber.NewError(fiber.StatusBadRequest, "该文件已加密，请输入导出时生成的密钥")
			}
			plain, err := secure.Unseal(in.Key, raw)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, err.Error())
			}
			payload = plain
		} else if strings.TrimSpace(in.Key) != "" {
			return fiber.NewError(fiber.StatusBadRequest, "该文件未加密，无需填写密钥")
		}

		var items []profile.Profile
		if err := json.Unmarshal(payload, &items); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "文件格式不正确，应为会话数组")
		}
		count, err := p.store.Import(items)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true, "imported": count})
	})

	api.Post("/test", p.handleTest)

	api.Get("/ws", websocket.New(p.handleWS))
}

func (p *SSH) handleTest(c *fiber.Ctx) error {
	var in struct {
		ID      string           `json:"id"`
		Profile *profile.Profile `json:"profile"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}

	var prof profile.Profile
	if in.Profile != nil && in.Profile.Host != "" {
		prof = *in.Profile
	} else {
		found, ok := p.store.Get(in.ID)
		if !ok {
			return fiber.NewError(fiber.StatusBadRequest, "请提供服务器的连接信息")
		}
		prof = found
	}

	jump := p.resolveJump(prof)
	start := time.Now()
	client, err := sshtools.DialVia(prof, jump)
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "error": err.Error()})
	}
	defer client.Close()
	latency := time.Since(start).Milliseconds()

	sess, err := client.NewSession()
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "error": "创建会话失败: " + err.Error()})
	}
	defer sess.Close()
	out, err := sess.CombinedOutput("echo devos-ok")
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "latencyMs": latency, "error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"ok":        true,
		"latencyMs": latency,
		"echo":      strings.TrimSpace(string(out)),
	})
}

func (p *SSH) resolveJump(prof profile.Profile) *profile.Profile {
	if prof.JumpProfileID == "" {
		return nil
	}
	jump, ok := p.store.Get(prof.JumpProfileID)
	if !ok || jump.ID == prof.ID {
		return nil
	}
	return &jump
}

type inMsg struct {
	T    string `json:"t"`
	D    string `json:"d"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type outMsg struct {
	T    string `json:"t"`
	D    string `json:"d,omitempty"`
	Code *int   `json:"code,omitempty"`
}

func (p *SSH) handleWS(c *websocket.Conn) {
	defer c.Close()

	var wmu sync.Mutex
	writeJSON := func(m outMsg) error {
		wmu.Lock()
		defer wmu.Unlock()
		return c.WriteJSON(m)
	}
	sendErr := func(text string) {
		_ = writeJSON(outMsg{T: "error", D: text})
	}

	prof, ok := p.store.Get(c.Query("profile"))
	if !ok {
		sendErr("会话不存在，请先在会话库中新建")
		return
	}

	_ = writeJSON(outMsg{T: "status", D: "正在建立 SSH 连接…"})

	jump := p.resolveJump(prof)
	client, err := sshtools.DialVia(prof, jump)
	if err != nil {
		sendErr("连接失败: " + err.Error())
		return
	}
	defer client.Close()

	stopKeepAlive := sshtools.StartKeepAlive(client, time.Duration(prof.KeepAliveSec)*time.Second)
	defer stopKeepAlive()

	cols := queryInt(c, "cols", 110)
	rows := queryInt(c, "rows", 32)
	if cols < 20 {
		cols = 80
	}
	if rows < 5 {
		rows = 24
	}

	enc := sshtools.LookupEncoding(prof.EncodingOrDefault())
	startup, _ := encodeText(enc, prof.StartupCmd)
	shell, err := sshtools.StartShell(client, prof.TerminalOrDefault(), cols, rows, startup)
	if err != nil {
		sendErr(err.Error())
		return
	}
	defer shell.Session.Close()

	logFile, logPath := p.openSessionLog(prof)
	if logFile != nil {
		defer logFile.Close()
	}

	encName := prof.EncodingOrDefault()
	ready := outMsg{T: "ready", D: encName}
	if logPath != "" {
		ready = outMsg{T: "ready", D: encName + "|" + logPath}
	}
	_ = writeJSON(ready)

	var stdout io.Reader = shell.Stdout
	if enc != nil {
		stdout = transform.NewReader(shell.Stdout, enc.NewDecoder())
	}
	if logFile != nil {
		stdout = io.TeeReader(stdout, logFile)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				wmu.Lock()
				werr := c.WriteMessage(websocket.BinaryMessage, append([]byte(nil), buf[:n]...))
				wmu.Unlock()
				if werr != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		code := sshtools.ExitCode(shell.Session.Wait())
		_ = writeJSON(outMsg{T: "exit", Code: &code})
		c.Close()
	}()

	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.TextMessage {
			continue
		}
		var m inMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "i":
			if m.D == "" {
				continue
			}
			payload, err := encodeText(enc, m.D)
			if err != nil {
				continue
			}
			_, _ = shell.Stdin.Write(payload)
		case "r":
			if m.Rows > 0 && m.Cols > 0 {
				_ = shell.Session.WindowChange(m.Rows, m.Cols)
			}
		case "p":
			_ = writeJSON(outMsg{T: "pong"})
		}
	}

	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
	}
}

func encodeText(enc encoding.Encoding, text string) ([]byte, error) {
	if enc == nil || text == "" {
		return []byte(text), nil
	}
	return enc.NewEncoder().Bytes([]byte(text))
}

func (p *SSH) openSessionLog(prof profile.Profile) (*os.File, string) {
	if !prof.LogEnabled {
		return nil, ""
	}
	dir := filepath.Join(p.dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		p.log.Warn("创建日志目录失败", "err", err)
		return nil, ""
	}
	name := sanitize(prof.Name)
	if name == "" {
		name = prof.Host
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.log", name, time.Now().Format("20060102-150405")))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		p.log.Warn("创建会话日志失败", "err", err)
		return nil, ""
	}
	return file, path
}

func sanitize(name string) string {
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	return replacer.Replace(strings.TrimSpace(name))
}

func queryInt(c *websocket.Conn, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}
