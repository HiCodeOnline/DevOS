// Package dbapp 提供数据库连接工具（MySQL / PostgreSQL / SQLite）。
package dbapp

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

//go:embed web
var webFS embed.FS

// Connection 描述一个数据库连接配置（参照 Navicat 的连接属性）。
type Connection struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group,omitempty"`
	Driver   string `json:"driver"` // mysql | postgres | sqlite
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
	File     string `json:"file,omitempty"` // SQLite 数据库文件
	Charset  string `json:"charset,omitempty"`
	SSLMode  string `json:"sslMode,omitempty"`
	Params   string `json:"params,omitempty"` // 额外 DSN 参数，k=v&k2=v2
	ReadOnly bool   `json:"readOnly,omitempty"`
	Color    string `json:"color,omitempty"`
}

// ApplyDefaults 补齐连接配置的默认值。
func (c *Connection) ApplyDefaults() {
	if c.Driver == "" {
		c.Driver = "mysql"
	}
	t := typeOf(c.Driver)
	if t.File {
		c.Port = 0
	} else if c.Port == 0 {
		c.Port = t.DefaultPort
	}
	switch t.Family {
	case familyMySQL:
		if c.Charset == "" {
			c.Charset = "utf8mb4"
		}
	case familyPostgres:
		if c.SSLMode == "" {
			c.SSLMode = "disable"
		}
	}
	if c.Name == "" {
		if t.File {
			c.Name = filepath.Base(c.File)
		} else {
			c.Name = c.Host
		}
	}
}

// DB 是数据库工具插件实例。
type DB struct {
	mu    sync.RWMutex
	conns []Connection
	file  string
	log   *slog.Logger

	poolsMu sync.Mutex
	pools   map[string]*sqlPool
}

// New 创建数据库插件。
func New() *DB { return &DB{pools: map[string]*sqlPool{}} }

func (p *DB) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "db",
		Name:        "数据库",
		Description: "连接 MySQL / PostgreSQL / SQLite 及国产化、云数据库",
		Version:     "0.1.0",
		Category:    "开发者",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5.5" rx="8" ry="3"/><path d="M4 5.5v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/><path d="M4 11.5v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/></svg>`,
		Accent:      "#10b981",
		Entry:       "index.html",
		Width:       1240,
		Height:      780,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *DB) Init(ctx *plugin.Context) error {
	p.file = filepath.Join(ctx.DataDir, "connections.json")
	p.log = ctx.Logger
	return p.load()
}

func (p *DB) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *DB) Register(api fiber.Router) {
	api.Get("/types", func(c *fiber.Ctx) error {
		return c.JSON(dbTypes)
	})
	api.Get("/connections", func(c *fiber.Ctx) error {
		return c.JSON(p.listConnections())
	})
	api.Post("/connections", p.handleSaveConnection)
	api.Delete("/connections/:id", p.handleDeleteConnection)
	api.Post("/connections/:id/duplicate", p.handleDuplicateConnection)
	api.Get("/connections/export", p.handleExportConnections)
	api.Post("/connections/import", p.handleImportConnections)
	api.Post("/test", p.handleTest)
	api.Get("/ping", p.handlePing)
	api.Get("/databases", p.handleDatabases)
	api.Get("/schemas", p.handleSchemas)
	api.Get("/objects", p.handleObjects)
	api.Get("/columns", p.handleColumns)
	api.Get("/table-data", p.handleTableData)
	api.Post("/query", p.handleQuery)
	api.Post("/export", p.handleExport)
}

func (p *DB) listConnections() []Connection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := append([]Connection{}, p.conns...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// conn 按 ID 查找连接配置。
func (p *DB) conn(id string) (Connection, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, c := range p.conns {
		if c.ID == id {
			return c, nil
		}
	}
	return Connection{}, fmt.Errorf("连接不存在")
}

func (p *DB) handleSaveConnection(c *fiber.Ctx) error {
	var in Connection
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Driver == "" {
		in.Driver = "mysql"
	}
	t, ok := dbTypeIndex[in.Driver]
	if !ok {
		return jsonErr(c, fiber.StatusBadRequest, "不支持的数据库类型")
	}
	if t.File {
		in.File = strings.TrimSpace(in.File)
		if in.File == "" {
			return jsonErr(c, fiber.StatusBadRequest, "SQLite 数据库文件不能为空")
		}
	} else if strings.TrimSpace(in.Host) == "" {
		return jsonErr(c, fiber.StatusBadRequest, "主机地址不能为空")
	}
	in.ApplyDefaults()

	saved, err := p.saveConnection(in)
	if err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(saved)
}

func (p *DB) saveConnection(in Connection) (Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	isNew := in.ID == ""
	if isNew {
		in.ID = newID()
	}
	found := false
	for i := range p.conns {
		if p.conns[i].ID == in.ID {
			p.conns[i] = in
			found = true
			break
		}
	}
	if !found {
		p.conns = append(p.conns, in)
	}
	// 配置变化后关闭旧连接池，下次访问时按新配置重连。
	p.closePools(in.ID)
	return in, p.persistLocked()
}

func (p *DB) handleDeleteConnection(c *fiber.Ctx) error {
	id := c.Params("id")
	p.mu.Lock()
	for i := range p.conns {
		if p.conns[i].ID == id {
			p.conns = append(p.conns[:i], p.conns[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	p.closePools(id)
	if err := p.persist(); err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *DB) handleDuplicateConnection(c *fiber.Ctx) error {
	src, err := p.conn(c.Params("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusNotFound, err.Error())
	}
	src.ID = ""
	src.Name = src.Name + " 副本"
	saved, err := p.saveConnection(src)
	if err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(saved)
}

func (p *DB) handleExportConnections(c *fiber.Ctx) error {
	data, err := json.MarshalIndent(p.listConnections(), "", "  ")
	if err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="devos-databases.json"`)
	return c.Send(data)
}

func (p *DB) handleImportConnections(c *fiber.Ctx) error {
	var items []Connection
	if err := json.Unmarshal(c.Body(), &items); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "文件格式不正确，应为连接数组")
	}
	p.mu.Lock()
	ids := make(map[string]bool, len(p.conns))
	for _, conn := range p.conns {
		ids[conn.ID] = true
	}
	count := 0
	for _, conn := range items {
		if conn.Driver == "" {
			conn.Driver = "mysql"
		}
		t, ok := dbTypeIndex[conn.Driver]
		if !ok {
			continue
		}
		if t.File && conn.File == "" {
			continue
		}
		if !t.File && conn.Host == "" {
			continue
		}
		if conn.ID == "" || ids[conn.ID] {
			conn.ID = newID()
			conn.Name = conn.Name + " (导入)"
		}
		conn.ApplyDefaults()
		ids[conn.ID] = true
		p.conns = append(p.conns, conn)
		count++
	}
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true, "imported": count})
}

func (p *DB) load() error {
	data, err := os.ReadFile(p.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.Unmarshal(data, &p.conns)
}

func (p *DB) persist() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.persistLocked()
}

// persistLocked 在已持有写锁时写入配置文件。
func (p *DB) persistLocked() error {
	data, err := json.MarshalIndent(p.conns, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.file, append(data, '\n'), 0o600)
}

func jsonErr(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg})
}

func newID() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
