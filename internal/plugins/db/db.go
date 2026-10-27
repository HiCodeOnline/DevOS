// Package dbapp 提供数据库连接工具（MySQL / PostgreSQL / SQLite）。
package dbapp

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
	"devos/internal/secure"
	"devos/internal/sqlstore"
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
	case familyRedis:
		if strings.TrimSpace(c.Database) == "" {
			c.Database = "0"
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
	db    *sql.DB
	keys  *secure.Keyring
	log   *slog.Logger

	poolsMu      sync.Mutex
	pools        map[string]*sqlPool
	redisClients map[string]*redisPool
	mongoClients map[string]*mongoConn

	nativeMu sync.Mutex
	native   map[string]any
}

var connMigrations = []string{`
CREATE TABLE connections (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL DEFAULT '',
	grp        TEXT NOT NULL DEFAULT '',
	driver     TEXT NOT NULL DEFAULT '',
	host       TEXT NOT NULL DEFAULT '',
	port       INTEGER NOT NULL DEFAULT 0,
	user       TEXT NOT NULL DEFAULT '',
	password   TEXT NOT NULL DEFAULT '',
	database   TEXT NOT NULL DEFAULT '',
	file       TEXT NOT NULL DEFAULT '',
	charset    TEXT NOT NULL DEFAULT '',
	ssl_mode   TEXT NOT NULL DEFAULT '',
	params     TEXT NOT NULL DEFAULT '',
	read_only  INTEGER NOT NULL DEFAULT 0,
	color      TEXT NOT NULL DEFAULT ''
);
`}

const (
	connColumns = `id, name, grp, driver, host, port, user, password, database,
	file, charset, ssl_mode, params, read_only, color`

	insertConn = `INSERT INTO connections (` + connColumns + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
)

// New 创建数据库插件。
func New() *DB {
	return &DB{
		pools:        map[string]*sqlPool{},
		redisClients: map[string]*redisPool{},
		mongoClients: map[string]*mongoConn{},
		native:       map[string]any{},
	}
}

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
	p.log = ctx.Logger
	keys, err := secure.Open(ctx.DataDir, "connections.key")
	if err != nil {
		return fmt.Errorf("初始化连接加密密钥失败: %w", err)
	}
	p.keys = keys
	db, err := sqlstore.Open(ctx.DataDir, "connections.db", connMigrations)
	if err != nil {
		return fmt.Errorf("打开连接数据库失败: %w", err)
	}
	p.db = db
	return p.load(ctx.DataDir)
}

// Close 关闭连接数据库，主要供测试与插件卸载使用。
func (p *DB) Close() error {
	return p.db.Close()
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
	api.Post("/connections/export", p.handleExportConnections)
	api.Post("/connections/import", p.handleImportConnections)
	api.Post("/test", p.handleTest)
	api.Get("/ping", p.handlePing)
	api.Get("/databases", p.handleDatabases)
	api.Get("/schemas", p.handleSchemas)
	api.Get("/objects", p.handleObjects)
	api.Get("/columns", p.handleColumns)
	api.Get("/table-data", p.handleTableData)
	api.Get("/table-structure", p.handleTableStructure)
	api.Post("/table-rows", p.handleTableRows)
	api.Post("/table-alter", p.handleTableAlter)
	api.Post("/query", p.handleQuery)
	api.Post("/explain", p.handleExplain)
	api.Post("/sqlfile", p.handleSQLFile)
	api.Post("/export", p.handleExport)

	// NoSQL：Redis 键空间与 MongoDB 文档浏览。
	api.Get("/redis/keys", p.handleRedisKeys)
	api.Get("/redis/key", p.handleRedisKey)
	api.Delete("/redis/key", p.handleRedisDeleteKey)
	api.Post("/redis/command", p.handleRedisCommand)
	api.Get("/mongo/collections", p.handleMongoCollections)
	api.Get("/mongo/documents", p.handleMongoDocuments)
	api.Post("/mongo/command", p.handleMongoCommand)
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
	if t.Family == familyRedis && strings.TrimSpace(in.Database) != "" {
		if idx, err := strconv.Atoi(strings.TrimSpace(in.Database)); err != nil || idx < 0 {
			return jsonErr(c, fiber.StatusBadRequest, "Redis 库号必须是非负整数")
		}
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
	payload, err := json.Marshal(p.listConnections())
	if err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	password, err := secure.NewExportPassword()
	if err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	content, err := secure.Seal(password, payload)
	if err != nil {
		return jsonErr(c, fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{
		"filename": "devos-databases.json",
		"key":      password,
		"content":  string(content),
	})
}

func (p *DB) handleImportConnections(c *fiber.Ctx) error {
	var in struct {
		Content string `json:"content"`
		Key     string `json:"key"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	raw := []byte(strings.TrimSpace(in.Content))
	if len(raw) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "导入文件内容为空")
	}
	payload := raw
	if secure.IsEnvelope(raw) {
		if strings.TrimSpace(in.Key) == "" {
			return jsonErr(c, fiber.StatusBadRequest, "该文件已加密，请输入导出时生成的密钥")
		}
		plain, err := secure.Unseal(in.Key, raw)
		if err != nil {
			return jsonErr(c, fiber.StatusBadRequest, err.Error())
		}
		payload = plain
	} else if strings.TrimSpace(in.Key) != "" {
		return jsonErr(c, fiber.StatusBadRequest, "该文件未加密，无需填写密钥")
	}

	var items []Connection
	if err := json.Unmarshal(payload, &items); err != nil {
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

func (p *DB) load(dir string) error {
	if err := p.migrateLegacy(filepath.Join(dir, "connections.json")); err != nil {
		return err
	}
	rows, err := p.db.Query("SELECT " + connColumns + " FROM connections")
	if err != nil {
		return err
	}
	defer rows.Close()
	conns := make([]Connection, 0, 8)
	for rows.Next() {
		var c Connection
		var readOnly int
		if err := rows.Scan(&c.ID, &c.Name, &c.Group, &c.Driver, &c.Host, &c.Port, &c.User,
			&c.Password, &c.Database, &c.File, &c.Charset, &c.SSLMode, &c.Params,
			&readOnly, &c.Color); err != nil {
			return err
		}
		c.ReadOnly = readOnly != 0
		if secure.IsEncrypted(c.Password) {
			plain, err := p.keys.Decrypt(c.Password)
			if err != nil {
				return fmt.Errorf("解密连接「%s」的密码失败: %w", c.Name, err)
			}
			c.Password = plain
		}
		conns = append(conns, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.conns = conns
	p.mu.Unlock()
	return nil
}

// migrateLegacy 把历史上的 connections.json 导入数据库，成功后保留 .bak 备份。
func (p *DB) migrateLegacy(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var count int
	if err := p.db.QueryRow("SELECT COUNT(*) FROM connections").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		var items []Connection
		if err := json.Unmarshal(data, &items); err != nil {
			return fmt.Errorf("解析连接配置失败: %w", err)
		}
		for i := range items {
			if !secure.IsEncrypted(items[i].Password) {
				continue
			}
			plain, err := p.keys.Decrypt(items[i].Password)
			if err != nil {
				return fmt.Errorf("解密连接「%s」的密码失败: %w", items[i].Name, err)
			}
			items[i].Password = plain
		}
		p.mu.Lock()
		p.conns = items
		err = p.persistLocked()
		p.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := sqlstore.BackupLegacy(path); err != nil && p.log != nil {
		p.log.Warn("备份历史连接配置失败", "err", err)
	}
	return nil
}

func (p *DB) persist() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.persistLocked()
}

// persistLocked 在已持有写锁时把内存中的连接配置同步到数据库。
func (p *DB) persistLocked() error {
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM connections"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(insertConn)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range p.conns {
		encrypted, err := p.keys.Encrypt(p.conns[i].Password)
		if err != nil {
			return err
		}
		c := p.conns[i]
		if _, err := stmt.Exec(c.ID, c.Name, c.Group, c.Driver, c.Host, c.Port, c.User,
			encrypted, c.Database, c.File, c.Charset, c.SSLMode, c.Params,
			boolInt(c.ReadOnly), c.Color); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func jsonErr(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg})
}

func newID() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
