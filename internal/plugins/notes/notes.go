// Package notes 提供类 Notion 的笔记应用：页面树、块编辑器、搜索、收藏与回收站。
package notes

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
	"devos/internal/secure"
)

//go:embed web
var webFS embed.FS

const (
	maxPages         = 5000
	maxBlocksPerPage = 600
	maxContentRunes  = 20000
	maxUploadBytes   = 3 << 20
	maxSearchResults = 50
)

// 支持的块类型，与前端编辑器保持一致。
var blockTypes = map[string]bool{
	"paragraph": true, "heading1": true, "heading2": true, "heading3": true,
	"bulleted": true, "numbered": true, "todo": true, "toggle": true,
	"quote": true, "callout": true, "code": true, "divider": true,
	"image": true, "bookmark": true, "table": true,
	"equation": true, "diagram": true,
}

var blockColors = map[string]bool{
	"default": true, "gray": true, "brown": true, "orange": true,
	"yellow": true, "green": true, "blue": true, "purple": true,
	"pink": true, "red": true,
}

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
}

// BlockProps 保存不同块类型的附加属性。
type BlockProps struct {
	Checked   bool       `json:"checked,omitempty"`
	Collapsed bool       `json:"collapsed,omitempty"`
	Language  string     `json:"language,omitempty"`
	Icon      string     `json:"icon,omitempty"`
	Color     string     `json:"color,omitempty"`
	URL       string     `json:"url,omitempty"`
	Caption   string     `json:"caption,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	HasHeader bool       `json:"hasHeader,omitempty"`
}

// Block 是页面中的一个内容块。
type Block struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Content string     `json:"content"`
	Props   BlockProps `json:"props"`
}

// Page 是一个笔记页面，支持无限层级嵌套。
type Page struct {
	ID        string  `json:"id"`
	ParentID  string  `json:"parentId"`
	Title     string  `json:"title"`
	Icon      string  `json:"icon"`
	Cover     string  `json:"cover"`
	Favorite  bool    `json:"favorite"`
	Trashed   bool    `json:"trashed"`
	TrashedAt string  `json:"trashedAt"`
	Order     int     `json:"order"`
	Blocks    []Block `json:"blocks"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

// PageSummary 是侧边栏与搜索结果使用的页面摘要（不含块内容）。
type PageSummary struct {
	ID         string `json:"id"`
	ParentID   string `json:"parentId"`
	Title      string `json:"title"`
	Icon       string `json:"icon"`
	Cover      string `json:"cover"`
	Favorite   bool   `json:"favorite"`
	Trashed    bool   `json:"trashed"`
	TrashedAt  string `json:"trashedAt"`
	Order      int    `json:"order"`
	BlockCount int    `json:"blockCount"`
	Preview    string `json:"preview"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// Workspace 是全部笔记数据。
type Workspace struct {
	Pages []Page `json:"pages"`
}

// SearchResult 是一条搜索结果。
type SearchResult struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Icon      string `json:"icon"`
	Snippet   string `json:"snippet"`
	Match     string `json:"match"` // title | content
	UpdatedAt string `json:"updatedAt"`
}

type API struct {
	mu       sync.RWMutex
	ws       Workspace
	file     string
	filesDir string
	keys     *secure.Keyring
	log      *slog.Logger
}

func New() *API { return &API{} }

func (p *API) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "notes",
		Name:        "笔记",
		Description: "类 Notion 的块编辑器，支持页面树、搜索、收藏与 Markdown 导入导出",
		Version:     "1.0.0",
		Category:    "效率",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3h8l4 4v14a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 16h6M9 8h2"/></svg>`,
		Accent:      "#d97706",
		Entry:       "index.html",
		Width:       1280,
		Height:      820,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *API) Init(ctx *plugin.Context) error {
	p.file = filepath.Join(ctx.DataDir, "notes.json")
	p.filesDir = filepath.Join(ctx.DataDir, "files")
	p.log = ctx.Logger
	if err := os.MkdirAll(p.filesDir, 0o755); err != nil {
		return fmt.Errorf("创建附件目录失败: %w", err)
	}
	keys, err := secure.Open(ctx.DataDir, "notes.key")
	if err != nil {
		return fmt.Errorf("初始化加密密钥失败: %w", err)
	}
	p.keys = keys
	if err := p.readEncrypted(p.file, &p.ws, "笔记数据"); err != nil {
		return err
	}
	p.normalizeWorkspace()
	return p.persist()
}

func (p *API) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *API) Register(api fiber.Router) {
	api.Get("/workspace", p.handleWorkspace)
	api.Get("/search", p.handleSearch)
	api.Post("/pages", p.handleCreatePage)
	api.Get("/pages/:id", p.handleGetPage)
	api.Put("/pages/:id", p.handleSavePage)
	api.Delete("/pages/:id", p.handleDeletePage)
	api.Post("/pages/:id/favorite", p.handleFavorite)
	api.Post("/pages/:id/move", p.handleMove)
	api.Post("/pages/:id/trash", p.handleTrash)
	api.Post("/pages/:id/restore", p.handleRestore)
	api.Post("/pages/:id/duplicate", p.handleDuplicate)
	api.Get("/pages/:id/export", p.handleExport)
	api.Delete("/trash", p.handleEmptyTrash)
	api.Post("/upload", p.handleUpload)
	api.Get("/files/:name", p.handleFile)
}

func (p *API) handleWorkspace(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	p.mu.RLock()
	defer p.mu.RUnlock()
	pages := make([]PageSummary, 0, len(p.ws.Pages))
	for _, page := range sortedPages(p.ws.Pages) {
		pages = append(pages, summarize(page))
	}
	return c.JSON(fiber.Map{"pages": pages})
}

func (p *API) handleGetPage(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	p.mu.RLock()
	defer p.mu.RUnlock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	return c.JSON(page)
}

type createInput struct {
	ParentID string  `json:"parentId"`
	Title    string  `json:"title"`
	Icon     string  `json:"icon"`
	Cover    string  `json:"cover"`
	Blocks   []Block `json:"blocks"`
}

func (p *API) handleCreatePage(c *fiber.Ctx) error {
	var in createInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	p.mu.Lock()
	if len(p.ws.Pages) >= maxPages {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusBadRequest, "页面数量已达上限")
	}
	parent := strings.TrimSpace(in.ParentID)
	if parent != "" {
		if target := p.findPage(parent); target == nil || target.Trashed {
			parent = ""
		}
	}
	now := time.Now().Format(time.RFC3339)
	page := Page{
		ID:        newID(),
		ParentID:  parent,
		Title:     normalizeTitle(in.Title),
		Icon:      normalizeIcon(in.Icon),
		Cover:     normalizeCover(in.Cover),
		Order:     p.nextOrder(parent),
		Blocks:    normalizeBlocks(in.Blocks),
		CreatedAt: now,
		UpdatedAt: now,
	}
	p.ws.Pages = append(p.ws.Pages, page)
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(page)
}

type saveInput struct {
	Title  *string  `json:"title"`
	Icon   *string  `json:"icon"`
	Cover  *string  `json:"cover"`
	Blocks *[]Block `json:"blocks"`
}

func (p *API) handleSavePage(c *fiber.Ctx) error {
	var in saveInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	if in.Title != nil {
		page.Title = normalizeTitle(*in.Title)
	}
	if in.Icon != nil {
		page.Icon = normalizeIcon(*in.Icon)
	}
	if in.Cover != nil {
		page.Cover = normalizeCover(*in.Cover)
	}
	if in.Blocks != nil {
		page.Blocks = normalizeBlocks(*in.Blocks)
	}
	page.UpdatedAt = time.Now().Format(time.RFC3339)
	result := *page
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(result)
}

func (p *API) handleFavorite(c *fiber.Ctx) error {
	var in struct {
		Value *bool `json:"value"`
	}
	_ = c.BodyParser(&in)
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	if in.Value != nil {
		page.Favorite = *in.Value
	} else {
		page.Favorite = !page.Favorite
	}
	page.UpdatedAt = time.Now().Format(time.RFC3339)
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleMove(c *fiber.Ctx) error {
	var in struct {
		ParentID string `json:"parentId"`
		Index    int    `json:"index"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	parent := strings.TrimSpace(in.ParentID)
	if parent == page.ID {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusBadRequest, "页面不能移动到自身之下")
	}
	if parent != "" {
		target := p.findPage(parent)
		if target == nil || target.Trashed {
			p.mu.Unlock()
			return fiber.NewError(fiber.StatusBadRequest, "目标页面不可用")
		}
		if p.isDescendant(parent, page.ID) {
			p.mu.Unlock()
			return fiber.NewError(fiber.StatusBadRequest, "页面不能移动到自身的子页面之下")
		}
	}
	oldParent := page.ParentID
	list := p.siblings(parent, page.ID)
	index := in.Index
	if index < 0 || index > len(list) {
		index = len(list)
	}
	list = append(list, nil)
	copy(list[index+1:], list[index:])
	list[index] = page
	page.ParentID = parent
	stamp := nowString()
	for i, sibling := range list {
		if sibling != nil {
			sibling.Order = i
			sibling.UpdatedAt = stamp
		}
	}
	if oldParent != parent {
		p.reindex(oldParent)
	}
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleTrash(c *fiber.Ctx) error {
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	stamp := nowString()
	for _, id := range p.subtreeIDs(page.ID) {
		if item := p.findPage(id); item != nil {
			item.Trashed = true
			item.TrashedAt = stamp
			item.Favorite = false
		}
	}
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleRestore(c *fiber.Ctx) error {
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	parent := p.findPage(page.ParentID)
	if page.ParentID != "" && (parent == nil || parent.Trashed) {
		page.ParentID = ""
		page.Order = p.nextOrder("")
	}
	for _, id := range p.subtreeIDs(page.ID) {
		if item := p.findPage(id); item != nil {
			item.Trashed = false
			item.TrashedAt = ""
		}
	}
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleDuplicate(c *fiber.Ctx) error {
	var in struct {
		Title string `json:"title"`
	}
	_ = c.BodyParser(&in)
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	if len(p.ws.Pages)+len(p.subtreeIDs(page.ID)) > maxPages {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusBadRequest, "页面数量已达上限")
	}
	ids := p.subtreeIDs(page.ID)
	now := nowString()
	mapping := make(map[string]string, len(ids))
	for _, id := range ids {
		mapping[id] = newID()
	}
	// 原页面之后的同级页面顺延，让副本紧跟在原页面之后。
	for _, sibling := range p.siblings(page.ParentID, "") {
		if sibling.Order > page.Order {
			sibling.Order++
		}
	}
	copied := make([]Page, 0, len(ids))
	for _, id := range ids {
		source := p.findPage(id)
		if source == nil {
			continue
		}
		target := *source
		target.ID = mapping[id]
		target.Favorite = false
		target.Trashed = false
		target.TrashedAt = ""
		target.CreatedAt = now
		target.UpdatedAt = now
		target.Blocks = cloneBlocks(source.Blocks)
		if id == page.ID {
			if title := strings.TrimSpace(in.Title); title != "" {
				target.Title = normalizeTitle(title)
			} else {
				target.Title = normalizeTitle(source.Title + " (copy)")
			}
			target.Order = page.Order + 1
		}
		if next, ok := mapping[source.ParentID]; ok {
			target.ParentID = next
		} else {
			target.ParentID = source.ParentID
		}
		copied = append(copied, target)
	}
	p.ws.Pages = append(p.ws.Pages, copied...)
	p.reindex(page.ParentID)
	rootID := mapping[page.ID]
	p.mu.Unlock()
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true, "pageId": rootID})
}

func (p *API) handleDeletePage(c *fiber.Ctx) error {
	p.mu.Lock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	ids := p.subtreeIDs(page.ID)
	remove := make(map[string]bool, len(ids))
	var files []string
	for _, id := range ids {
		remove[id] = true
		if item := p.findPage(id); item != nil {
			files = append(files, pageFiles(*item)...)
		}
	}
	out := p.ws.Pages[:0]
	for _, item := range p.ws.Pages {
		if !remove[item.ID] {
			out = append(out, item)
		}
	}
	p.ws.Pages = out
	p.reindex(page.ParentID)
	p.mu.Unlock()
	for _, name := range files {
		_ = os.Remove(filepath.Join(p.filesDir, name))
	}
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleEmptyTrash(c *fiber.Ctx) error {
	p.mu.Lock()
	var files []string
	out := p.ws.Pages[:0]
	for _, page := range p.ws.Pages {
		if page.Trashed {
			files = append(files, pageFiles(page)...)
			continue
		}
		out = append(out, page)
	}
	p.ws.Pages = out
	p.mu.Unlock()
	for _, name := range files {
		_ = os.Remove(filepath.Join(p.filesDir, name))
	}
	if err := p.persist(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (p *API) handleSearch(c *fiber.Ctx) error {
	query := strings.TrimSpace(c.Query("q"))
	c.Set("Cache-Control", "no-store")
	if query == "" {
		return c.JSON([]SearchResult{})
	}
	lower := strings.ToLower(query)
	p.mu.RLock()
	defer p.mu.RUnlock()
	results := make([]SearchResult, 0, 16)
	for _, page := range sortedPages(p.ws.Pages) {
		if page.Trashed {
			continue
		}
		item := SearchResult{ID: page.ID, Title: page.Title, Icon: page.Icon, UpdatedAt: page.UpdatedAt}
		if strings.Contains(strings.ToLower(page.Title), lower) {
			item.Match = "title"
			item.Snippet = summarize(page).Preview
			results = append(results, item)
			if len(results) >= maxSearchResults {
				break
			}
			continue
		}
		for _, block := range page.Blocks {
			text := blockPlainText(block)
			if text == "" || !strings.Contains(strings.ToLower(text), lower) {
				continue
			}
			item.Match = "content"
			item.Snippet = makeSnippet(text, query)
			results = append(results, item)
			break
		}
		if len(results) >= maxSearchResults {
			break
		}
	}
	return c.JSON(results)
}

func (p *API) handleExport(c *fiber.Ctx) error {
	p.mu.RLock()
	page := p.findPage(c.Params("id"))
	if page == nil {
		p.mu.RUnlock()
		return fiber.NewError(fiber.StatusNotFound, "页面不存在")
	}
	content := exportMarkdown(*page)
	title := page.Title
	if title == "" {
		title = "Untitled"
	}
	p.mu.RUnlock()
	filename := safeFilename(title) + ".md"
	c.Set("Content-Type", "text/markdown; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="note.md"; filename*=UTF-8''`+encodeFilename(filename))
	return c.SendString(content)
}

func (p *API) handleUpload(c *fiber.Ctx) error {
	file, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "未收到上传文件")
	}
	if file.Size <= 0 || file.Size > maxUploadBytes {
		return fiber.NewError(fiber.StatusBadRequest, "图片大小需在 3MB 以内")
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !imageExts[ext] {
		return fiber.NewError(fiber.StatusBadRequest, "仅支持 PNG/JPG/GIF/WebP/BMP 图片")
	}
	src, err := file.Open()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "读取上传文件失败")
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(src, head)
	_ = src.Close()
	if !strings.HasPrefix(http.DetectContentType(head[:n]), "image/") {
		return fiber.NewError(fiber.StatusBadRequest, "文件内容不是有效图片")
	}
	name := newID() + ext
	if err := c.SaveFile(file, filepath.Join(p.filesDir, name)); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "保存图片失败")
	}
	return c.JSON(fiber.Map{"url": "/api/apps/notes/files/" + name})
}

var fileNamePattern = regexp.MustCompile(`^[a-f0-9]{16}\.(png|jpg|jpeg|gif|webp|bmp)$`)

func (p *API) handleFile(c *fiber.Ctx) error {
	name := c.Params("name")
	if !fileNamePattern.MatchString(name) {
		return fiber.ErrNotFound
	}
	path := filepath.Join(p.filesDir, name)
	if _, err := os.Stat(path); err != nil {
		return fiber.ErrNotFound
	}
	c.Set("Cache-Control", "public, max-age=31536000, immutable")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.SendFile(path)
}

// findPage 在已持锁的情况下按 ID 查找页面。
func (p *API) findPage(id string) *Page {
	for i := range p.ws.Pages {
		if p.ws.Pages[i].ID == id {
			return &p.ws.Pages[i]
		}
	}
	return nil
}

// siblings 返回同一层级下的页面（可按 ID 排除），保持现有顺序。
func (p *API) siblings(parentID, exclude string) []*Page {
	var out []*Page
	for i := range p.ws.Pages {
		page := &p.ws.Pages[i]
		if page.Trashed || page.ParentID != parentID || page.ID == exclude {
			continue
		}
		out = append(out, page)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order == out[j].Order {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Order < out[j].Order
	})
	return out
}

// reindex 重排某个层级下的顺序号。
func (p *API) reindex(parentID string) {
	for i, page := range p.siblings(parentID, "") {
		page.Order = i
	}
}

func (p *API) nextOrder(parentID string) int {
	out := p.siblings(parentID, "")
	if len(out) == 0 {
		return 0
	}
	return out[len(out)-1].Order + 1
}

// subtreeIDs 返回页面自身及全部后代的 ID。
func (p *API) subtreeIDs(root string) []string {
	children := make(map[string][]string)
	for _, page := range p.ws.Pages {
		children[page.ParentID] = append(children[page.ParentID], page.ID)
	}
	var out []string
	queue := []string{root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, id)
		queue = append(queue, children[id]...)
	}
	return out
}

// isDescendant 判断 candidate 是否为 ancestor 的后代。
func (p *API) isDescendant(candidate, ancestor string) bool {
	seen := map[string]bool{}
	current := candidate
	for current != "" && !seen[current] {
		seen[current] = true
		if current == ancestor {
			return true
		}
		page := p.findPage(current)
		if page == nil {
			return false
		}
		current = page.ParentID
	}
	return false
}

func (p *API) normalizeWorkspace() {
	if p.ws.Pages == nil {
		p.ws.Pages = []Page{}
	}
	// 去重并丢弃非法 ID，保证后续查找稳定。
	seen := make(map[string]bool, len(p.ws.Pages))
	pages := make([]Page, 0, len(p.ws.Pages))
	for _, page := range p.ws.Pages {
		if !validID(page.ID) || seen[page.ID] {
			page.ID = newID()
		}
		if seen[page.ID] {
			continue
		}
		seen[page.ID] = true
		page.Title = normalizeTitle(page.Title)
		page.Icon = normalizeIcon(page.Icon)
		page.Cover = normalizeCover(page.Cover)
		page.Blocks = normalizeBlocks(page.Blocks)
		pages = append(pages, page)
		if len(pages) >= maxPages {
			break
		}
	}
	p.ws.Pages = pages
	// 父页面不存在或形成环时回退到根层级。
	for i := range p.ws.Pages {
		page := &p.ws.Pages[i]
		if page.ParentID == page.ID {
			page.ParentID = ""
			continue
		}
		if page.ParentID != "" && p.findPage(page.ParentID) == nil {
			page.ParentID = ""
		}
	}
	for i := range p.ws.Pages {
		page := &p.ws.Pages[i]
		current := page.ParentID
		for depth := 0; current != "" && depth < maxPages; depth++ {
			if current == page.ID {
				page.ParentID = ""
				break
			}
			parent := p.findPage(current)
			if parent == nil {
				page.ParentID = ""
				break
			}
			current = parent.ParentID
		}
	}
}

func normalizeBlocks(blocks []Block) []Block {
	if blocks == nil {
		blocks = []Block{}
	}
	if len(blocks) > maxBlocksPerPage {
		blocks = blocks[:maxBlocksPerPage]
	}
	out := make([]Block, 0, len(blocks)+1)
	seen := map[string]bool{}
	for _, block := range blocks {
		if !validID(block.ID) || seen[block.ID] {
			block.ID = newID()
		}
		seen[block.ID] = true
		if !blockTypes[block.Type] {
			block.Type = "paragraph"
		}
		block.Content = normalizeContent(block.Type, block.Content)
		block.Props = normalizeProps(block.Type, block.Props)
		out = append(out, block)
	}
	if len(out) == 0 {
		out = append(out, Block{ID: newID(), Type: "paragraph", Content: ""})
	}
	return out
}

func normalizeContent(blockType, content string) string {
	// 代码、分割线、公式与流程图的正文是原始源码，不做行内 HTML 清洗。
	if blockType == "code" || blockType == "divider" || blockType == "equation" || blockType == "diagram" {
		return truncateRunes(content, maxContentRunes)
	}
	safe := sanitizeInline(content)
	return truncateRunes(safe, maxContentRunes)
}

func normalizeProps(blockType string, props BlockProps) BlockProps {
	props.Icon = truncateRunes(strings.TrimSpace(props.Icon), 16)
	if props.Icon == "" && blockType == "callout" {
		props.Icon = "💡"
	}
	if !blockColors[props.Color] {
		props.Color = "default"
	}
	props.Language = truncateRunes(strings.TrimSpace(props.Language), 20)
	if props.Language == "" && blockType == "code" {
		props.Language = "text"
	}
	props.Caption = truncateRunes(strings.TrimSpace(props.Caption), 500)
	props.URL = truncateRunes(strings.TrimSpace(props.URL), 2000)
	if props.URL != "" && !safeAssetURL(props.URL) {
		props.URL = ""
	}
	if blockType == "table" {
		rows := props.Rows
		if len(rows) == 0 {
			rows = [][]string{{"", "", ""}, {"", "", ""}, {"", "", ""}}
		}
		if len(rows) > 20 {
			rows = rows[:20]
		}
		for i := range rows {
			if len(rows[i]) > 10 {
				rows[i] = rows[i][:10]
			}
			for j := range rows[i] {
				rows[i][j] = truncateRunes(strings.TrimSpace(rows[i][j]), 500)
			}
		}
		props.Rows = rows
	} else {
		props.Rows = nil
		props.HasHeader = false
	}
	return props
}

func normalizeTitle(title string) string {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	return truncateRunes(title, 200)
}

func normalizeIcon(icon string) string {
	return truncateRunes(strings.TrimSpace(icon), 16)
}

// normalizeCover 只接受内置渐变色与本地/远程图片地址。
func normalizeCover(cover string) string {
	cover = strings.TrimSpace(cover)
	if cover == "" {
		return ""
	}
	if strings.HasPrefix(cover, "gradient:") {
		return truncateRunes(cover, 200)
	}
	if safeAssetURL(cover) {
		return truncateRunes(cover, 2000)
	}
	return ""
}

func summarize(page Page) PageSummary {
	preview := ""
	for _, block := range page.Blocks {
		text := blockPlainText(block)
		if text != "" {
			preview = truncateRunes(text, 120)
			break
		}
	}
	return PageSummary{
		ID:         page.ID,
		ParentID:   page.ParentID,
		Title:      page.Title,
		Icon:       page.Icon,
		Cover:      page.Cover,
		Favorite:   page.Favorite,
		Trashed:    page.Trashed,
		TrashedAt:  page.TrashedAt,
		Order:      page.Order,
		BlockCount: len(page.Blocks),
		Preview:    preview,
		CreatedAt:  page.CreatedAt,
		UpdatedAt:  page.UpdatedAt,
	}
}

func sortedPages(pages []Page) []Page {
	out := make([]Page, len(pages))
	copy(out, pages)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ParentID == out[j].ParentID {
			if out[i].Order == out[j].Order {
				return out[i].CreatedAt < out[j].CreatedAt
			}
			return out[i].Order < out[j].Order
		}
		return out[i].ParentID < out[j].ParentID
	})
	return out
}

func blockPlainText(block Block) string {
	switch block.Type {
	case "code":
		return strings.TrimSpace(block.Content)
	case "table":
		var parts []string
		for _, row := range block.Props.Rows {
			for _, cell := range row {
				if cell != "" {
					parts = append(parts, cell)
				}
			}
		}
		return strings.Join(parts, " ")
	case "image", "bookmark":
		return strings.TrimSpace(block.Props.Caption)
	default:
		return plainText(block.Content)
	}
}

func pageFiles(page Page) []string {
	var out []string
	add := func(value string) {
		value = strings.TrimPrefix(value, "files/")
		if strings.HasPrefix(value, "/api/apps/notes/files/") {
			value = strings.TrimPrefix(value, "/api/apps/notes/files/")
		}
		if fileNamePattern.MatchString(value) {
			out = append(out, value)
		}
	}
	add(page.Cover)
	for _, block := range page.Blocks {
		if block.Type == "image" {
			add(block.Props.URL)
		}
	}
	return out
}

func cloneBlocks(blocks []Block) []Block {
	out := make([]Block, len(blocks))
	for i, block := range blocks {
		out[i] = block
		if block.Props.Rows != nil {
			rows := make([][]string, len(block.Props.Rows))
			for j, row := range block.Props.Rows {
				rows[j] = append([]string(nil), row...)
			}
			out[i].Props.Rows = rows
		}
	}
	return out
}

func makeSnippet(text, query string) string {
	byteIndex := strings.Index(strings.ToLower(text), strings.ToLower(query))
	if byteIndex < 0 {
		return truncateRunes(text, 140)
	}
	runes := []rune(text)
	index := utf8.RuneCountInString(text[:byteIndex])
	start := index - 40
	if start < 0 {
		start = 0
	}
	end := index + utf8.RuneCountInString(query) + 80
	if end > len(runes) {
		end = len(runes)
	}
	part := string(runes[start:end])
	if start > 0 {
		part = "…" + part
	}
	if end < len(runes) {
		part += "…"
	}
	return strings.TrimSpace(part)
}

// 行内标签白名单与清洗规则。
var (
	tagPattern    = regexp.MustCompile(`(?s)<[^>]*>`)
	hrefPattern   = regexp.MustCompile(`(?i)\bhref\s*=\s*("([^"]*)"|'([^']*)')`)
	allowedTags   = map[string]bool{"b": true, "strong": true, "i": true, "em": true, "u": true, "s": true, "strike": true, "del": true, "code": true, "a": true, "br": true, "mark": true}
	mdLinkPattern = regexp.MustCompile(`(?s)<a href="([^"]*)">(.*?)</a>`)
)

// sanitizeInline 只保留编辑器需要的行内标签，避免注入脚本与样式。
func sanitizeInline(raw string) string {
	var out strings.Builder
	last := 0
	for _, loc := range tagPattern.FindAllStringIndex(raw, -1) {
		out.WriteString(raw[last:loc[0]])
		out.WriteString(cleanTag(raw[loc[0]:loc[1]]))
		last = loc[1]
	}
	out.WriteString(raw[last:])
	return out.String()
}

func cleanTag(tag string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(tag), "<"), ">")
	body = strings.TrimSpace(body)
	closing := strings.HasPrefix(body, "/")
	body = strings.TrimPrefix(body, "/")
	body = strings.TrimSpace(strings.TrimSuffix(body, "/"))
	name := body
	if i := strings.IndexAny(name, " \t\r\n"); i >= 0 {
		name = name[:i]
	}
	name = strings.ToLower(name)
	if !allowedTags[name] {
		return ""
	}
	if name == "br" {
		return "<br>"
	}
	if closing {
		if name == "br" {
			return ""
		}
		return "</" + name + ">"
	}
	if name == "a" {
		if m := hrefPattern.FindStringSubmatch(tag); m != nil {
			href := m[2]
			if href == "" {
				href = m[3]
			}
			if safeURL(href) {
				return `<a href="` + html.EscapeString(href) + `">`
			}
		}
		return ""
	}
	return "<" + name + ">"
}

func safeURL(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "mailto:") {
		return true
	}
	return false
}

// safeAssetURL 额外允许插件自身上传的图片地址。
func safeAssetURL(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/api/apps/notes/files/") || strings.HasPrefix(value, "files/") {
		return true
	}
	return safeURL(value)
}

func plainText(value string) string {
	text := tagPattern.ReplaceAllString(value, " ")
	text = html.UnescapeString(text)
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

// inlineToMarkdown 将受控的行内 HTML 转换为 Markdown。
func inlineToMarkdown(value string) string {
	value = mdLinkPattern.ReplaceAllString(value, "[$2]($1)")
	value = strings.NewReplacer(
		"<br>", "  \n",
		"<strong>", "**", "</strong>", "**",
		"<b>", "**", "</b>", "**",
		"<em>", "*", "</em>", "*",
		"<i>", "*", "</i>", "*",
		"<del>", "~~", "</del>", "~~",
		"<s>", "~~", "</s>", "~~",
		"<strike>", "~~", "</strike>", "~~",
		"<code>", "`", "</code>", "`",
		"<mark>", "==", "</mark>", "==",
	).Replace(value)
	value = tagPattern.ReplaceAllString(value, "")
	return html.UnescapeString(value)
}

func exportMarkdown(page Page) string {
	var out strings.Builder
	title := page.Title
	if title == "" {
		title = "Untitled"
	}
	out.WriteString("# " + strings.ReplaceAll(title, "\n", " ") + "\n\n")
	for _, block := range page.Blocks {
		text := inlineToMarkdown(block.Content)
		switch block.Type {
		case "heading1":
			out.WriteString("# " + text + "\n\n")
		case "heading2":
			out.WriteString("## " + text + "\n\n")
		case "heading3":
			out.WriteString("### " + text + "\n\n")
		case "bulleted":
			out.WriteString("- " + text + "\n")
		case "numbered":
			out.WriteString("1. " + text + "\n")
		case "todo":
			if block.Props.Checked {
				out.WriteString("- [x] " + text + "\n")
			} else {
				out.WriteString("- [ ] " + text + "\n")
			}
		case "toggle":
			out.WriteString("- " + text + "\n")
		case "quote":
			out.WriteString("> " + text + "\n\n")
		case "callout":
			out.WriteString("> " + block.Props.Icon + " " + text + "\n\n")
		case "code":
			lang := block.Props.Language
			if lang == "text" {
				lang = ""
			}
			out.WriteString("```" + lang + "\n" + block.Content + "\n```\n\n")
		case "divider":
			out.WriteString("---\n\n")
		case "equation":
			src := strings.TrimSpace(block.Content)
			if strings.Contains(src, "\n") {
				out.WriteString("$$\n" + src + "\n$$\n\n")
			} else {
				out.WriteString("$$" + src + "$$\n\n")
			}
		case "diagram":
			out.WriteString("```mermaid\n" + block.Content + "\n```\n\n")
		case "image":
			out.WriteString("![" + block.Props.Caption + "](" + block.Props.URL + ")\n\n")
		case "bookmark":
			label := block.Props.Caption
			if label == "" {
				label = block.Props.URL
			}
			out.WriteString("[" + label + "](" + block.Props.URL + ")\n\n")
		case "table":
			out.WriteString(tableMarkdown(block))
		default:
			if text != "" {
				out.WriteString(text + "\n\n")
			}
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}

func tableMarkdown(block Block) string {
	rows := block.Props.Rows
	if len(rows) == 0 {
		return ""
	}
	var out strings.Builder
	for i, row := range rows {
		out.WriteString("|")
		for _, cell := range row {
			out.WriteString(" " + strings.ReplaceAll(cell, "|", `\|`) + " |")
		}
		out.WriteString("\n")
		if i == 0 && block.Props.HasHeader {
			out.WriteString("|")
			for range row {
				out.WriteString(" --- |")
			}
			out.WriteString("\n")
		}
	}
	out.WriteString("\n")
	return out.String()
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

func (p *API) persist() error {
	p.mu.RLock()
	ws := p.ws
	p.mu.RUnlock()
	return p.writeEncrypted(p.file, ws)
}

func newID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{4,64}$`)

func validID(id string) bool { return idPattern.MatchString(id) }

func nowString() string { return time.Now().Format(time.RFC3339) }

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func safeFilename(title string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = "note"
	}
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_").Replace(name)
	return truncateRunes(name, 80)
}

func encodeFilename(name string) string {
	var out strings.Builder
	for _, b := range []byte(name) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '.' || b == '-' || b == '_' {
			out.WriteByte(b)
			continue
		}
		out.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{b})))
	}
	return out.String()
}
