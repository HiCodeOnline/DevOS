// Package files 提供文件管理：按分组归档开发与工作中的文件，
// 添加时先比对 SHA-256 去重，复制后再计算一次校验，落盘目录按年/月/日分层，
// 并在窗口右侧快速预览图片、文本等类型的文件。
package files

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
	"devos/internal/platform"
	"devos/internal/sqlstore"
)

//go:embed web
var webFS embed.FS

const (
	dbName         = "files.db"
	storageDirName = "storage"

	previewTextMax = 512 * 1024 // 文本预览最多返回 512KB
	maxNameRunes   = 120
	maxTagsPerFile = 12
	maxTagRunes    = 24
	maxNoteRunes   = 2000
	maxGroupRunes  = 60

	defaultGroupName = "默认分组"
	defaultColor     = "#0ea5e9"
)

var groupColors = map[string]bool{
	"#0ea5e9": true, "#6366f1": true, "#10b981": true, "#f59e0b": true,
	"#f43f5e": true, "#8b5cf6": true, "#14b8a6": true, "#64748b": true,
}

var fileMigrations = []string{`
CREATE TABLE groups (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL DEFAULT '',
	color      TEXT NOT NULL DEFAULT '',
	ord        INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE files (
	id          TEXT PRIMARY KEY,
	group_id    TEXT NOT NULL DEFAULT '',
	name        TEXT NOT NULL DEFAULT '',
	path        TEXT NOT NULL DEFAULT '',
	size        INTEGER NOT NULL DEFAULT 0,
	sha256      TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL DEFAULT '',
	mime        TEXT NOT NULL DEFAULT '',
	tags        TEXT NOT NULL DEFAULT '[]',
	date        TEXT NOT NULL DEFAULT '',
	source      TEXT NOT NULL DEFAULT '',
	keep_source INTEGER NOT NULL DEFAULT 0,
	note        TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT '',
	updated_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_files_sha ON files(sha256);
CREATE INDEX idx_files_group ON files(group_id, date);
`}

// Group 是文件分组。
type Group struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Order int    `json:"order"`
	Count int    `json:"count"`
}

// FileRecord 是文件库中的一条记录。
type FileRecord struct {
	ID         string   `json:"id"`
	GroupID    string   `json:"groupId"`
	GroupName  string   `json:"groupName"`
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	AbsPath    string   `json:"absPath,omitempty"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
	Kind       string   `json:"kind"`
	MIME       string   `json:"mime"`
	Tags       []string `json:"tags"`
	Date       string   `json:"date"`
	Source     string   `json:"source"`
	KeepSource bool     `json:"keepSource"`
	Note       string   `json:"note"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

// TagCount 用于侧边栏的标签统计。
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// API 实现 plugin.Plugin。
type API struct {
	db          *sql.DB
	log         *slog.Logger
	storageRoot string
}

// New 创建文件管理实例。
func New() *API {
	return &API{}
}

func (p *API) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "files",
		Name:        "文件管理",
		Description: "集中归档开发与工作中的文件，支持分组、标签、SHA-256 去重校验与快速预览",
		Version:     "1.0.0",
		Category:    "效率",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/><path d="M3 10h18"/></svg>`,
		Accent:      "#0ea5e9",
		Entry:       "index.html",
		Width:       1260,
		Height:      820,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *API) Init(ctx *plugin.Context) error {
	p.log = ctx.Logger
	if err := os.MkdirAll(ctx.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建文件库目录失败: %w", err)
	}
	db, err := sqlstore.Open(ctx.DataDir, dbName, fileMigrations)
	if err != nil {
		return fmt.Errorf("打开文件库失败: %w", err)
	}
	p.db = db
	p.storageRoot = filepath.Join(ctx.DataDir, storageDirName)
	if err := os.MkdirAll(p.storageRoot, 0o755); err != nil {
		return fmt.Errorf("创建文件存放目录失败: %w", err)
	}
	return p.ensureDefaultGroup()
}

// Close 关闭文件库数据库，主要供测试与插件卸载使用。
func (p *API) Close() error {
	return p.db.Close()
}

func (p *API) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *API) Register(api fiber.Router) {
	api.Get("/meta", p.handleMeta)
	api.Get("/list", p.handleList)
	api.Post("/pick", p.handlePick)
	api.Post("/add", p.handleAdd)

	api.Post("/groups", p.handleCreateGroup)
	api.Put("/groups/:id", p.handleUpdateGroup)
	api.Delete("/groups/:id", p.handleDeleteGroup)

	api.Put("/files/:id", p.handleUpdateFile)
	api.Delete("/files/:id", p.handleDeleteFile)
	api.Post("/files/:id/reveal", p.handleRevealFile)
	api.Get("/files/:id/raw", p.handleRawFile)
}

func (p *API) ensureDefaultGroup() error {
	var count int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groups`).Scan(&count); err != nil {
		return fmt.Errorf("读取分组失败: %w", err)
	}
	if count > 0 {
		return nil
	}
	_, err := p.db.Exec(`INSERT INTO groups (id, name, color, ord, created_at) VALUES (?, ?, ?, 0, ?)`,
		newID(), defaultGroupName, defaultColor, nowString())
	return err
}

// ---------- 查询 ----------

const fileColumns = `f.id, f.group_id, COALESCE(g.name, ''), f.name, f.path, f.size, f.sha256,
	f.kind, f.mime, f.tags, f.date, f.source, f.keep_source, f.note, f.created_at, f.updated_at`

func (p *API) queryFiles(where string, args ...any) ([]FileRecord, error) {
	query := `SELECT ` + fileColumns + ` FROM files f LEFT JOIN groups g ON g.id = f.group_id` +
		where + ` ORDER BY f.date DESC, f.created_at DESC, f.name`
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FileRecord, 0, 16)
	for rows.Next() {
		rec, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		rec.AbsPath = p.resolve(rec.Path)
		out = append(out, rec)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFile(row rowScanner) (FileRecord, error) {
	var (
		rec        FileRecord
		tagsRaw    string
		keepSource int
	)
	if err := row.Scan(&rec.ID, &rec.GroupID, &rec.GroupName, &rec.Name, &rec.Path, &rec.Size,
		&rec.SHA256, &rec.Kind, &rec.MIME, &tagsRaw, &rec.Date, &rec.Source, &keepSource,
		&rec.Note, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return FileRecord{}, err
	}
	rec.KeepSource = keepSource != 0
	rec.Tags = parseTags(tagsRaw)
	return rec, nil
}

func (p *API) getFile(id string) (FileRecord, error) {
	list, err := p.queryFiles(` WHERE f.id = ?`, id)
	if err != nil {
		return FileRecord{}, err
	}
	if len(list) == 0 {
		return FileRecord{}, sql.ErrNoRows
	}
	return list[0], nil
}

func (p *API) findBySHA(sha string) (FileRecord, bool, error) {
	list, err := p.queryFiles(` WHERE f.sha256 = ?`, sha)
	if err != nil {
		return FileRecord{}, false, err
	}
	if len(list) == 0 {
		return FileRecord{}, false, nil
	}
	return list[0], true, nil
}

func (p *API) loadGroups() ([]Group, error) {
	rows, err := p.db.Query(`SELECT g.id, g.name, g.color, g.ord,
		(SELECT COUNT(*) FROM files f WHERE f.group_id = g.id)
		FROM groups g ORDER BY g.ord, g.created_at, g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Group, 0, 8)
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Color, &g.Order, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *API) loadTagStats() ([]TagCount, int, int64, error) {
	rows, err := p.db.Query(`SELECT tags, size FROM files`)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	counts := map[string]int{}
	count := 0
	var total int64
	for rows.Next() {
		var (
			tagsRaw string
			size    int64
		)
		if err := rows.Scan(&tagsRaw, &size); err != nil {
			return nil, 0, 0, err
		}
		count++
		total += size
		for _, tag := range parseTags(tagsRaw) {
			counts[tag]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	tags := make([]TagCount, 0, len(counts))
	for name, n := range counts {
		tags = append(tags, TagCount{Name: name, Count: n})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Name < tags[j].Name
	})
	return tags, count, total, nil
}

func (p *API) countUngrouped() (int, error) {
	var n int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM files WHERE group_id = ''`).Scan(&n)
	return n, err
}

func (p *API) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(p.storageRoot, filepath.FromSlash(path))
}

// ---------- 元信息 ----------

func (p *API) handleMeta(c *fiber.Ctx) error {
	groups, err := p.loadGroups()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	tags, count, total, err := p.loadTagStats()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件统计失败")
	}
	ungrouped, err := p.countUngrouped()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件统计失败")
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{
		"groups":     groups,
		"tags":       tags,
		"count":      count,
		"totalSize":  total,
		"ungrouped":  ungrouped,
		"storageDir": p.storageRoot,
	})
}

func (p *API) handleList(c *fiber.Ctx) error {
	groupID := strings.TrimSpace(c.Query("groupId"))
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	tag := strings.TrimSpace(c.Query("tag"))
	kind := strings.TrimSpace(c.Query("kind"))
	from := strings.TrimSpace(c.Query("from"))
	to := strings.TrimSpace(c.Query("to"))

	all, err := p.queryFiles("")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件列表失败")
	}
	out := make([]FileRecord, 0, len(all))
	for _, rec := range all {
		if groupID != "" {
			if groupID == "none" {
				if rec.GroupID != "" {
					continue
				}
			} else if rec.GroupID != groupID {
				continue
			}
		}
		if kind != "" && rec.Kind != kind {
			continue
		}
		if from != "" && rec.Date < from {
			continue
		}
		if to != "" && rec.Date > to {
			continue
		}
		if tag != "" && !containsString(rec.Tags, tag) {
			continue
		}
		if query != "" && !matchQuery(rec, query) {
			continue
		}
		out = append(out, rec)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{"files": out, "count": len(out)})
}

func matchQuery(rec FileRecord, query string) bool {
	if strings.Contains(strings.ToLower(rec.Name), query) ||
		strings.Contains(strings.ToLower(rec.Note), query) ||
		strings.Contains(strings.ToLower(rec.SHA256), query) {
		return true
	}
	for _, tag := range rec.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}

// ---------- 选择文件 ----------

func (p *API) handlePick(c *fiber.Ctx) error {
	var in struct {
		Start  string `json:"start"`
		Filter string `json:"filter"`
	}
	_ = c.BodyParser(&in)
	path, err := platform.PickFile(strings.TrimSpace(in.Start), strings.TrimSpace(in.Filter))
	if err != nil {
		// 用户取消选择不是错误，直接返回空路径。
		if strings.Contains(err.Error(), "未选择") {
			return c.JSON(fiber.Map{"path": ""})
		}
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(fiber.Map{"path": path})
}

// ---------- 添加文件 ----------

type addInput struct {
	Path       string   `json:"path"`
	GroupID    string   `json:"groupId"`
	Date       string   `json:"date"`
	Tags       []string `json:"tags"`
	KeepSource bool     `json:"keepSource"`
	Note       string   `json:"note"`
	Force      bool     `json:"force"`
}

func (p *API) handleAdd(c *fiber.Ctx) error {
	var in addInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	source := strings.TrimSpace(in.Path)
	if source == "" {
		return fiber.NewError(fiber.StatusBadRequest, "请选择文件路径")
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "文件路径无效")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "文件不存在或无法访问")
	}
	if !info.Mode().IsRegular() {
		return fiber.NewError(fiber.StatusBadRequest, "仅支持添加单个文件")
	}

	groupID, err := p.validateGroup(in.GroupID)
	if err != nil {
		return err
	}
	date, err := normalizeDate(in.Date)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	tags := normalizeTags(in.Tags)
	note := truncateRunes(strings.TrimSpace(in.Note), maxNoteRunes)

	sha, _, err := hashFile(abs)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "计算文件 SHA-256 失败: "+err.Error())
	}
	existing, found, err := p.findBySHA(sha)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "查询重复文件失败")
	}
	if found && !in.Force {
		c.Set("Cache-Control", "no-store")
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":     "duplicate",
			"message":  "文件库中已存在内容相同的文件",
			"existing": existing,
		})
	}

	// 内容已存在时直接复用已归档的副本，避免重复占用磁盘。
	var (
		relPath string
		target  string
	)
	reused := false
	if found {
		absExisting := p.resolve(existing.Path)
		if st, err := os.Stat(absExisting); err == nil && st.Mode().IsRegular() {
			target = absExisting
			relPath = existing.Path
			reused = true
		}
	}
	if !reused {
		relPath, target, err = p.targetPath(date, info.Name(), sha)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "规划存放目录失败: "+err.Error())
		}
		if err := copyAndVerify(abs, target, sha); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "归档文件失败: "+err.Error())
		}
	}

	mimeType := detectMIME(info.Name())
	rec := FileRecord{
		ID:         newID(),
		GroupID:    groupID,
		Name:       sanitizeName(info.Name()),
		Path:       relPath,
		Size:       info.Size(),
		SHA256:     sha,
		Kind:       detectKind(info.Name(), mimeType),
		MIME:       mimeType,
		Tags:       tags,
		Date:       date,
		Source:     abs,
		KeepSource: in.KeepSource,
		Note:       note,
		CreatedAt:  nowString(),
		UpdatedAt:  nowString(),
	}
	if _, err := p.db.Exec(`INSERT INTO files
		(id, group_id, name, path, size, sha256, kind, mime, tags, date, source, keep_source, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.GroupID, rec.Name, rec.Path, rec.Size, rec.SHA256, rec.Kind, rec.MIME,
		marshalTags(rec.Tags), rec.Date, rec.Source, boolToInt(rec.KeepSource), rec.Note,
		rec.CreatedAt, rec.UpdatedAt); err != nil {
		if !reused {
			_ = os.Remove(target)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "写入文件记录失败")
	}

	// 不保留原文件：入库成功后移除源文件；源文件本身就是库内副本时跳过。
	if !rec.KeepSource {
		if !samePath(abs, target) {
			if err := os.Remove(abs); err != nil && p.log != nil {
				p.log.Warn("删除源文件失败", "path", abs, "err", err)
			}
		}
	}
	created, err := p.getFile(rec.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件记录失败")
	}
	return c.JSON(fiber.Map{"file": created})
}

// ---------- 更新与删除 ----------

type updateInput struct {
	GroupID string   `json:"groupId"`
	Date    string   `json:"date"`
	Tags    []string `json:"tags"`
	Note    string   `json:"note"`
}

func (p *API) handleUpdateFile(c *fiber.Ctx) error {
	rec, err := p.getFile(c.Params("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "文件记录不存在")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件记录失败")
	}
	var in updateInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	groupID, err := p.validateGroup(in.GroupID)
	if err != nil {
		return err
	}
	date := rec.Date
	if strings.TrimSpace(in.Date) != "" {
		date, err = normalizeDate(in.Date)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
	}
	tags := normalizeTags(in.Tags)
	note := truncateRunes(strings.TrimSpace(in.Note), maxNoteRunes)
	if _, err := p.db.Exec(`UPDATE files SET group_id = ?, date = ?, tags = ?, note = ?, updated_at = ? WHERE id = ?`,
		groupID, date, marshalTags(tags), note, nowString(), rec.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "更新文件记录失败")
	}
	updated, err := p.getFile(rec.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件记录失败")
	}
	return c.JSON(fiber.Map{"file": updated})
}

func (p *API) handleDeleteFile(c *fiber.Ctx) error {
	rec, err := p.getFile(c.Params("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "文件记录不存在")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件记录失败")
	}
	if _, err := p.db.Exec(`DELETE FROM files WHERE id = ?`, rec.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "删除文件记录失败")
	}
	var others int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM files WHERE path = ?`, rec.Path).Scan(&others); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "检查文件引用失败")
	}
	if others == 0 {
		if err := os.Remove(p.resolve(rec.Path)); err != nil && !os.IsNotExist(err) && p.log != nil {
			p.log.Warn("删除归档文件失败", "path", rec.Path, "err", err)
		}
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleRevealFile(c *fiber.Ctx) error {
	rec, err := p.getFile(c.Params("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "文件记录不存在")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件记录失败")
	}
	if err := platform.RevealInFolder(p.resolve(rec.Path)); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

// ---------- 预览与下载 ----------

func (p *API) handleRawFile(c *fiber.Ctx) error {
	rec, err := p.getFile(c.Params("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "文件记录不存在")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "读取文件记录失败")
	}
	abs := p.resolve(rec.Path)
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
		return fiber.NewError(fiber.StatusNotFound, "归档文件不存在，可能已被移除")
	}
	c.Set("Cache-Control", "no-cache")
	if c.Query("download") == "1" {
		c.Set("Content-Disposition", "attachment; filename="+url.PathEscape(rec.Name))
	}
	switch rec.Kind {
	case "text", "code":
		data, err := readPreview(abs)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "读取文件失败")
		}
		c.Set("Content-Type", textContentType(rec.MIME, data))
		return c.Send(data)
	default:
		f, err := os.Open(abs)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "读取文件失败")
		}
		contentType := rec.MIME
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Set("Content-Type", contentType)
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return fiber.NewError(fiber.StatusInternalServerError, "读取文件失败")
		}
		// fasthttp 读取完响应体流后会关闭该文件句柄。
		return c.SendStream(f, int(st.Size()))
	}
}

func readPreview(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, previewTextMax+1))
	if err != nil {
		return nil, err
	}
	if len(data) > previewTextMax {
		data = append(data[:previewTextMax], []byte("\n…")...)
	}
	if !utf8.Valid(data) {
		// 非法 UTF-8 时按二进制处理，避免前端显示乱码。
		return []byte("该文件包含二进制内容，暂不支持文本预览。"), nil
	}
	return data, nil
}

func textContentType(mimeType string, data []byte) string {
	lower := strings.ToLower(mimeType)
	if lower == "" || lower == "application/octet-stream" {
		if utf8.Valid(data) {
			return "text/plain; charset=utf-8"
		}
		return "application/octet-stream"
	}
	if strings.HasPrefix(lower, "text/") && !strings.Contains(lower, "charset") {
		return mimeType + "; charset=utf-8"
	}
	return mimeType
}

// ---------- 分组 ----------

func (p *API) handleCreateGroup(c *fiber.Ctx) error {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "分组名称不能为空")
	}
	if utf8.RuneCountInString(name) > maxGroupRunes {
		return fiber.NewError(fiber.StatusBadRequest, "分组名称过长")
	}
	var dup int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE name = ?`, name).Scan(&dup); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	if dup > 0 {
		return fiber.NewError(fiber.StatusConflict, "同名分组已存在")
	}
	var ord int
	if err := p.db.QueryRow(`SELECT COALESCE(MAX(ord), -1) + 1 FROM groups`).Scan(&ord); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	g := Group{ID: newID(), Name: name, Color: normalizeColor(in.Color), Order: ord}
	if _, err := p.db.Exec(`INSERT INTO groups (id, name, color, ord, created_at) VALUES (?, ?, ?, ?, ?)`,
		g.ID, g.Name, g.Color, g.Order, nowString()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "创建分组失败")
	}
	return c.JSON(fiber.Map{"group": g})
}

func (p *API) handleUpdateGroup(c *fiber.Ctx) error {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "分组名称不能为空")
	}
	if utf8.RuneCountInString(name) > maxGroupRunes {
		return fiber.NewError(fiber.StatusBadRequest, "分组名称过长")
	}
	var exists int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE id = ?`, c.Params("id")).Scan(&exists); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	if exists == 0 {
		return fiber.NewError(fiber.StatusNotFound, "分组不存在")
	}
	var dup int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE name = ? AND id <> ?`, name, c.Params("id")).Scan(&dup); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	if dup > 0 {
		return fiber.NewError(fiber.StatusConflict, "同名分组已存在")
	}
	if _, err := p.db.Exec(`UPDATE groups SET name = ?, color = ? WHERE id = ?`,
		name, normalizeColor(in.Color), c.Params("id")); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "更新分组失败")
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleDeleteGroup(c *fiber.Ctx) error {
	id := c.Params("id")
	var exists int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE id = ?`, id).Scan(&exists); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	if exists == 0 {
		return fiber.NewError(fiber.StatusNotFound, "分组不存在")
	}
	res, err := p.db.Exec(`UPDATE files SET group_id = '', updated_at = ? WHERE group_id = ?`, nowString(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "移动分组内文件失败")
	}
	if _, err := p.db.Exec(`DELETE FROM groups WHERE id = ?`, id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "删除分组失败")
	}
	moved, _ := res.RowsAffected()
	return c.JSON(fiber.Map{"ok": true, "moved": moved})
}

func (p *API) validateGroup(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	var exists int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM groups WHERE id = ?`, id).Scan(&exists); err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "读取分组失败")
	}
	if exists == 0 {
		return "", fiber.NewError(fiber.StatusBadRequest, "分组不存在")
	}
	return id, nil
}

// ---------- 工具函数 ----------

func newID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func nowString() string {
	return time.Now().Format(time.RFC3339)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(a, b)
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func normalizeDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().Format("2006-01-02"), nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", errors.New("日期格式应为 YYYY-MM-DD")
	}
	return t.Format("2006-01-02"), nil
}

func normalizeColor(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if groupColors[value] {
		return value
	}
	return defaultColor
}

func parseTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	return normalizeTags(tags)
}

func marshalTags(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func normalizeTags(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" || seen[tag] {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTagRunes {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) >= maxTagsPerFile {
			break
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func sanitizeName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range base {
		if r < 32 || r == 127 {
			continue
		}
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), " .")
	if out == "" {
		out = "file"
	}
	runes := []rune(out)
	if len(runes) > maxNameRunes {
		ext := filepath.Ext(out)
		stem := strings.TrimSuffix(out, ext)
		limit := maxNameRunes - utf8.RuneCountInString(ext)
		if limit < 8 {
			limit = 8
			ext = ""
		}
		stemRunes := []rune(stem)
		if len(stemRunes) > limit {
			stemRunes = stemRunes[:limit]
		}
		out = string(stemRunes) + ext
	}
	return out
}

// targetPath 按年/月/日规划存放路径，返回相对路径与磁盘绝对路径。
func (p *API) targetPath(date, name, sha string) (string, string, error) {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(p.storageRoot, day.Format("2006"), day.Format("01"), day.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	clean := sanitizeName(name)
	prefix := sha
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	ext := filepath.Ext(clean)
	stem := strings.TrimSuffix(clean, ext)
	candidate := filepath.Join(dir, prefix+"-"+clean)
	for i := 2; ; i++ {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				break
			}
			return "", "", err
		}
		if i > 9999 {
			return "", "", errors.New("同名文件过多，请重命名后重试")
		}
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%s-%d%s", prefix, stem, i, ext))
	}
	rel, err := filepath.Rel(p.storageRoot, candidate)
	if err != nil {
		return "", "", err
	}
	return filepath.ToSlash(rel), candidate, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// copyAndVerify 复制文件到目标路径，并重新计算副本的 SHA-256 校验是否与源文件一致。
func copyAndVerify(src, dst, want string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".devos-files-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return errors.New("复制后 SHA-256 校验不一致，文件可能已损坏或被篡改")
	}
	_ = os.Chmod(tmpName, 0o644)
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

func detectMIME(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if mt := mime.TypeByExtension(ext); mt != "" {
		return mt
	}
	switch ext {
	case ".iso":
		return "application/x-iso9660-image"
	case ".img":
		return "application/octet-stream"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".log", ".ini", ".conf", ".cfg":
		return "text/plain; charset=utf-8"
	case ".yaml", ".yml":
		return "text/yaml; charset=utf-8"
	case ".toml":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

var kindByExt = map[string]string{
	".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image",
	".webp": "image", ".bmp": "image", ".svg": "image", ".ico": "image",
	".tif": "image", ".tiff": "image", ".heic": "image", ".avif": "image",

	".txt": "text", ".md": "text", ".markdown": "text", ".log": "text",
	".json": "text", ".xml": "text", ".yaml": "text", ".yml": "text",
	".ini": "text", ".conf": "text", ".csv": "text", ".env": "text",

	".go": "code", ".js": "code", ".mjs": "code", ".ts": "code", ".tsx": "code",
	".jsx": "code", ".py": "code", ".java": "code", ".c": "code", ".h": "code",
	".cpp": "code", ".hpp": "code", ".cs": "code", ".rs": "code", ".rb": "code",
	".php": "code", ".sh": "code", ".bat": "code", ".ps1": "code", ".sql": "code",
	".html": "code", ".css": "code", ".scss": "code", ".vue": "code", ".lua": "code",
	".kt": "code", ".swift": "code", ".dart": "code", ".toml": "code", ".gradle": "code",

	".mp4": "video", ".mkv": "video", ".mov": "video", ".avi": "video",
	".webm": "video", ".flv": "video", ".wmv": "video", ".m4v": "video",

	".mp3": "audio", ".wav": "audio", ".flac": "audio", ".aac": "audio",
	".ogg": "audio", ".m4a": "audio", ".wma": "audio",

	".zip": "archive", ".rar": "archive", ".7z": "archive", ".tar": "archive",
	".gz": "archive", ".tgz": "archive", ".bz2": "archive", ".xz": "archive",
	".iso": "archive", ".img": "archive", ".dmg": "archive",

	".pdf": "doc", ".doc": "doc", ".docx": "doc", ".xls": "doc", ".xlsx": "doc",
	".ppt": "doc", ".pptx": "doc", ".odt": "doc", ".ods": "doc", ".odp": "doc",
	".epub": "doc", ".rtf": "doc",
}

func detectKind(name, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if kind, ok := kindByExt[ext]; ok {
		return kind
	}
	lower := strings.ToLower(mimeType)
	switch {
	case strings.HasPrefix(lower, "image/"):
		return "image"
	case strings.HasPrefix(lower, "video/"):
		return "video"
	case strings.HasPrefix(lower, "audio/"):
		return "audio"
	case strings.HasPrefix(lower, "text/"):
		return "text"
	}
	return "other"
}
