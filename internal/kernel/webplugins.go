package kernel

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

// Web 插件规范限制。规范说明见 docs/web-plugins.md。
const (
	webPluginSpecVersion = 1         // 内核当前支持的插件规范版本
	maxPluginZipSize     = 64 << 20  // 上传 zip 大小上限
	maxPluginFiles       = 20000     // zip 条目数上限
	maxPluginUnzipped    = 256 << 20 // 解压后总大小上限
)

// 插件 ID 同时用于安装目录名与 URL 路径，必须足够保守。
var pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// webApp 是一个已装载的运行时 Web 插件。
type webApp struct {
	manifest    plugin.Manifest
	dir         string    // 插件根目录（含 plugin.json）
	staticDir   string    // 静态资源目录绝对路径
	entry       string    // 入口文件，相对静态资源目录
	spa         bool      // 未命中的无扩展名路径回退到入口页
	iconFile    string    // icon 位于静态目录之外时的实际文件路径
	installedAt time.Time // 安装时间（目录修改时间）
}

// pluginInfo 是插件管理接口返回的扁平结构。
type pluginInfo struct {
	plugin.Manifest
	Source      string `json:"source"` // builtin | installed
	Dir         string `json:"dir,omitempty"`
	DataDir     string `json:"dataDir,omitempty"`
	InstalledAt string `json:"installedAt,omitempty"`
}

// ---------------------------------------------------------------------------
// 装载与校验
// ---------------------------------------------------------------------------

// scanWebPlugins 扫描目录下形如 <id>/plugin.json 的 Web 插件。
// 校验失败的插件只记录日志，不影响内核启动。
func (k *Kernel) scanWebPlugins(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			k.log.Warn("读取 Web 插件目录失败", "dir", dir, "err", err)
		}
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		base := filepath.Join(dir, name)
		wa, err := buildWebApp(base)
		if err != nil {
			k.log.Warn("跳过不合规的 Web 插件", "dir", base, "err", err)
			continue
		}
		if wa.manifest.ID != name {
			k.log.Warn("跳过不合规的 Web 插件", "dir", base, "err", fmt.Sprintf("目录名与插件 id %q 不一致", wa.manifest.ID))
			continue
		}
		if !k.registerWebApp(wa) {
			continue
		}
		k.log.Info("已装载 Web 插件", "id", wa.manifest.ID, "version", wa.manifest.Version, "dir", base)
	}
}

// registerWebApp 注册 Web 插件；内置 ID 与重复 ID 会被拒绝。
func (k *Kernel) registerWebApp(wa *webApp) bool {
	id := wa.manifest.ID
	if k.builtinIDs[id] {
		k.log.Warn("Web 插件与内置插件同名，已忽略", "id", id, "dir", wa.dir)
		return false
	}
	k.webMu.Lock()
	defer k.webMu.Unlock()
	if _, exists := k.webApps[id]; exists {
		k.log.Warn("Web 插件已存在，忽略重复装载", "id", id, "dir", wa.dir)
		return false
	}
	k.webApps[id] = wa
	return true
}

// buildWebApp 读取并校验插件清单，返回可装载的 Web 插件。
func buildWebApp(base string) (*webApp, error) {
	raw, err := os.ReadFile(filepath.Join(base, "plugin.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("缺少 plugin.json")
		}
		return nil, err
	}
	var m plugin.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("plugin.json 解析失败: %w", err)
	}

	m.ID = strings.TrimSpace(m.ID)
	if m.ID == "" {
		m.ID = filepath.Base(base)
	}
	if !pluginIDPattern.MatchString(m.ID) {
		return nil, fmt.Errorf("插件 id %q 不合法：仅允许小写字母、数字、点、下划线和短横线，长度 1-64", m.ID)
	}

	if m.SpecVersion == 0 {
		m.SpecVersion = webPluginSpecVersion
	}
	if m.SpecVersion < 1 || m.SpecVersion > webPluginSpecVersion {
		return nil, fmt.Errorf("插件规范版本 %d 不受支持（内核支持 1）", m.SpecVersion)
	}

	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Width <= 0 {
		m.Width = 960
	}
	if m.Height <= 0 {
		m.Height = 640
	}
	if strings.TrimSpace(m.Category) == "" {
		m.Category = "Web 插件"
	}
	m.Builtin = false
	m.Backend = false

	staticRoot, err := cleanRelativePath(m.StaticRoot)
	if err != nil {
		return nil, fmt.Errorf("staticRoot 不合法: %w", err)
	}
	if staticRoot == "" {
		staticRoot = "web"
	}
	m.StaticRoot = staticRoot
	staticDir := filepath.Join(base, filepath.FromSlash(staticRoot))
	if st, err := os.Stat(staticDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("缺少静态资源目录 %s/", staticRoot)
	}

	entry, err := cleanRelativePath(m.Entry)
	if err != nil {
		return nil, fmt.Errorf("entry 不合法: %w", err)
	}
	if entry == "" {
		entry = "index.html"
	}
	if st, err := os.Stat(filepath.Join(staticDir, filepath.FromSlash(entry))); err != nil || st.IsDir() {
		return nil, fmt.Errorf("入口文件 %s 不存在于 %s/", entry, staticRoot)
	}
	m.Entry = entry

	iconURL, iconFile, err := resolvePluginIcon(base, staticRoot, m.ID, m.Icon)
	if err != nil {
		return nil, err
	}
	m.Icon = iconURL

	installedAt := time.Now()
	if st, err := os.Stat(base); err == nil {
		installedAt = st.ModTime()
	}

	return &webApp{
		manifest:    m,
		dir:         base,
		staticDir:   staticDir,
		entry:       entry,
		spa:         m.SPA,
		iconFile:    iconFile,
		installedAt: installedAt,
	}, nil
}

// resolvePluginIcon 把 icon 规范化为可访问的 URL。
// 支持内联 SVG、data URL，以及插件内的相对图片路径（如 icon.png 或 web/icon.png）。
// 当图片位于静态资源目录之外时，通过 /apps/<id>/__icon 提供。
func resolvePluginIcon(base, staticRoot, id, icon string) (string, string, error) {
	icon = strings.TrimSpace(icon)
	if icon == "" || strings.HasPrefix(icon, "<") || strings.HasPrefix(icon, "data:") {
		return icon, "", nil
	}
	if strings.Contains(icon, "://") || strings.HasPrefix(icon, "/") {
		return "", "", errors.New("icon 必须是插件内的相对路径")
	}
	rel, err := cleanRelativePath(icon)
	if err != nil {
		return "", "", fmt.Errorf("icon 路径不合法: %w", err)
	}
	if rel == "" {
		return "", "", errors.New("icon 路径为空")
	}

	staticDir := filepath.Join(base, filepath.FromSlash(staticRoot))
	if st, err := os.Stat(filepath.Join(staticDir, filepath.FromSlash(rel))); err == nil && !st.IsDir() {
		return "/apps/" + url.PathEscape(id) + "/" + escapeURLPath(rel), "", nil
	}
	rootFile := filepath.Join(base, filepath.FromSlash(rel))
	if st, err := os.Stat(rootFile); err == nil && !st.IsDir() {
		return "/apps/" + url.PathEscape(id) + "/__icon", rootFile, nil
	}
	return "", "", fmt.Errorf("icon 文件不存在: %s", icon)
}

func escapeURLPath(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// cleanRelativePath 规范化相对路径，拒绝绝对路径与 .. 穿越。
func cleanRelativePath(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" || p == "." {
		return "", nil
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
		return "", errors.New("必须是相对路径")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", errors.New("不允许包含 ..")
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "/" {
		return "", nil
	}
	return strings.TrimPrefix(cleaned, "/"), nil
}

// ---------------------------------------------------------------------------
// 静态资源服务
// ---------------------------------------------------------------------------

// serveWebApp 提供运行时 Web 插件的静态资源，兼容 Next.js 静态导出布局。
func (k *Kernel) serveWebApp(c *fiber.Ctx) error {
	id := c.Params("appId")
	k.webMu.RLock()
	wa := k.webApps[id]
	k.webMu.RUnlock()
	if wa == nil {
		return fiber.NewError(fiber.StatusNotFound, "Web 插件未安装: "+id)
	}

	rel := strings.TrimPrefix(c.Params("*"), "/")
	if rel == "__icon" {
		if wa.iconFile == "" {
			return fiber.ErrNotFound
		}
		data, err := os.ReadFile(wa.iconFile)
		if err != nil {
			return fiber.ErrNotFound
		}
		c.Set("Cache-Control", "public, max-age=300")
		c.Set("Content-Type", contentTypeFor(wa.iconFile))
		return c.Send(data)
	}

	name, ok := wa.resolveAsset(rel)
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "资源不存在: "+rel)
	}

	full := filepath.Join(wa.staticDir, filepath.FromSlash(name))
	if !insideDir(wa.staticDir, full) {
		return fiber.ErrForbidden
	}
	// 读入内存后返回：避免 Windows 下打开的文件句柄阻塞插件目录覆盖与卸载。
	data, err := os.ReadFile(full)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "资源不存在: "+rel)
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm", ".json":
		c.Set("Cache-Control", "no-cache")
	default:
		c.Set("Cache-Control", "public, max-age=300")
	}
	c.Set("Content-Type", contentTypeFor(name))
	return c.Send(data)
}

// contentTypeFor 为静态资源推断 Content-Type。
func contentTypeFor(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return fiber.MIMETextHTMLCharsetUTF8
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return fiber.MIMEApplicationJSONCharsetUTF8
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".wasm":
		return "application/wasm"
	case ".txt", ".webmanifest":
		return "text/plain; charset=utf-8"
	default:
		return fiber.MIMEOctetStream
	}
}

// resolveAsset 把请求路径解析为实际文件。
// 无扩展名时依次尝试 <path>.html、<path>/index.html，最后按 SPA 配置回退入口页。
func (wa *webApp) resolveAsset(rel string) (string, bool) {
	clean, err := cleanRelativePath(rel)
	if err != nil {
		return "", false
	}
	if clean == "" {
		return wa.entry, true
	}

	candidates := []string{clean}
	if path.Ext(clean) == "" {
		candidates = []string{clean + ".html", path.Join(clean, "index.html"), clean}
	}
	for _, cand := range candidates {
		if st, err := os.Stat(filepath.Join(wa.staticDir, filepath.FromSlash(cand))); err == nil && !st.IsDir() {
			return cand, true
		}
	}
	if wa.spa {
		if st, err := os.Stat(filepath.Join(wa.staticDir, filepath.FromSlash(wa.entry))); err == nil && !st.IsDir() {
			return wa.entry, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// 插件管理 API
// ---------------------------------------------------------------------------

// handleListPlugins 返回内置与已安装插件的完整列表。
func (k *Kernel) handleListPlugins(c *fiber.Ctx) error {
	return c.JSON(k.pluginInfoList())
}

func (k *Kernel) pluginInfoList() []pluginInfo {
	out := make([]pluginInfo, 0, len(k.plugins)+len(k.webApps))
	for _, p := range k.plugins {
		m := p.Manifest()
		if m.Entry == "" {
			m.Entry = "index.html"
		}
		m.Builtin = true
		out = append(out, pluginInfo{
			Manifest: m,
			Source:   "builtin",
			DataDir:  filepath.Join(k.dataDir, "apps", m.ID),
		})
	}
	k.webMu.RLock()
	ids := make([]string, 0, len(k.webApps))
	for id := range k.webApps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, k.webInfo(k.webApps[id]))
	}
	k.webMu.RUnlock()
	return out
}

func (k *Kernel) webInfo(wa *webApp) pluginInfo {
	source := "installed"
	if !insideDir(k.webRoot, wa.dir) {
		source = "external"
	}
	return pluginInfo{
		Manifest:    wa.manifest,
		Source:      source,
		Dir:         wa.dir,
		DataDir:     filepath.Join(k.dataDir, "apps", wa.manifest.ID),
		InstalledAt: wa.installedAt.Format(time.RFC3339),
	}
}

// manifestList 返回桌面应用列表（内置 + Web 插件）。
func (k *Kernel) manifestList() []plugin.Manifest {
	infos := k.pluginInfoList()
	out := make([]plugin.Manifest, 0, len(infos))
	for _, info := range infos {
		m := info.Manifest
		if m.Entry == "" {
			m.Entry = "index.html"
		}
		out = append(out, m)
	}
	return out
}

func (k *Kernel) appCount() int {
	k.webMu.RLock()
	defer k.webMu.RUnlock()
	return len(k.plugins) + len(k.webApps)
}

func (k *Kernel) installedApp(id string) *webApp {
	k.webMu.RLock()
	defer k.webMu.RUnlock()
	return k.webApps[id]
}

// handleInstallPlugin 处理 ZIP 离线安装。
// 表单字段：file（必填，zip）、action（overwrite/keep/copy）、id（copy 时的新 id）。
func (k *Kernel) handleInstallPlugin(c *fiber.Ctx) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "请通过 file 字段上传 zip 插件包")
	}
	if fh.Size <= 0 || fh.Size > maxPluginZipSize {
		return fiber.NewError(fiber.StatusBadRequest, "插件包大小必须在 1 字节到 64MB 之间")
	}
	action := strings.ToLower(strings.TrimSpace(c.FormValue("action")))
	if action != "" && action != "overwrite" && action != "keep" && action != "copy" {
		return fiber.NewError(fiber.StatusBadRequest, "action 仅支持 overwrite/keep/copy")
	}
	if err := os.MkdirAll(k.webRoot, 0o755); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "无法创建插件目录: "+err.Error())
	}

	staging, err := os.MkdirTemp(k.webRoot, ".staging-")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "无法创建临时目录: "+err.Error())
	}
	defer os.RemoveAll(staging)

	zipPath := filepath.Join(staging, "plugin.zip")
	if err := saveUpload(fh, zipPath); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "读取上传文件失败: "+err.Error())
	}
	unpacked := filepath.Join(staging, "unpacked")
	if err := extractZip(zipPath, unpacked); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "解压失败: "+err.Error())
	}
	_ = os.Remove(zipPath)

	root, err := locatePluginRoot(unpacked)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	incoming, err := buildWebApp(root)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "插件校验失败: "+err.Error())
	}

	id := incoming.manifest.ID
	if k.builtinIDs[id] {
		return fiber.NewError(fiber.StatusConflict, "插件 id 与内置插件冲突："+id)
	}

	existing := k.installedApp(id)
	if existing != nil {
		switch action {
		case "":
			incomingInfo := k.webInfo(incoming)
			incomingInfo.Dir = ""
			incomingInfo.InstalledAt = ""
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error":    "插件已安装：" + existing.manifest.Name,
				"conflict": true,
				"existing": k.webInfo(existing),
				"incoming": incomingInfo,
				"relation": compareVersions(incoming.manifest.Version, existing.manifest.Version),
				"actions":  []string{"overwrite", "keep", "copy"},
			})
		case "keep":
			return c.JSON(fiber.Map{
				"status":  "kept",
				"id":      existing.manifest.ID,
				"name":    existing.manifest.Name,
				"version": existing.manifest.Version,
				"message": "已保留现有版本",
			})
		case "copy":
			newID := strings.TrimSpace(c.FormValue("id"))
			if !pluginIDPattern.MatchString(newID) {
				return fiber.NewError(fiber.StatusBadRequest, "另存安装需要提供合法的新 id（小写字母、数字、点、下划线、短横线）")
			}
			if k.builtinIDs[newID] {
				return fiber.NewError(fiber.StatusConflict, "新 id 与内置插件冲突："+newID)
			}
			if k.installedApp(newID) != nil {
				return fiber.NewError(fiber.StatusConflict, "新 id 已被占用："+newID)
			}
			if err := rewritePluginID(root, newID); err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, "改写插件 id 失败: "+err.Error())
			}
			incoming, err = buildWebApp(root)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "插件校验失败: "+err.Error())
			}
			id = newID
		}
	}

	target := filepath.Join(k.webRoot, id)
	backup := ""
	if existing != nil && action == "overwrite" {
		if !insideDir(k.webRoot, existing.dir) {
			return fiber.NewError(fiber.StatusConflict, "该插件来自外部插件目录，无法覆盖安装，请选择保留或另存")
		}
		backupDir := filepath.Join(k.webRoot, ".backup")
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "无法创建备份目录: "+err.Error())
		}
		backup = filepath.Join(backupDir, fmt.Sprintf("%s-%d", id, time.Now().UnixNano()))
		if err := os.Rename(existing.dir, backup); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "备份现有插件失败: "+err.Error())
		}
	}

	if err := os.Rename(root, target); err != nil {
		if backup != "" {
			_ = os.Rename(backup, existing.dir)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "写入插件目录失败: "+err.Error())
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}

	finalApp, err := buildWebApp(target)
	if err != nil {
		_ = os.RemoveAll(target)
		if backup != "" {
			_ = os.Rename(backup, existing.dir)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "安装后校验失败: "+err.Error())
	}

	k.webMu.Lock()
	k.webApps[id] = finalApp
	k.webMu.Unlock()

	// 为插件准备数据目录（覆盖安装与卸载均保留该目录）。
	if err := os.MkdirAll(filepath.Join(k.dataDir, "apps", id), 0o755); err != nil {
		k.log.Warn("创建插件数据目录失败", "id", id, "err", err)
	}
	k.log.Info("已安装 Web 插件", "id", id, "version", finalApp.manifest.Version, "action", action)

	return c.JSON(fiber.Map{
		"status":  "installed",
		"action":  action,
		"plugin":  k.webInfo(finalApp),
		"message": "插件已安装",
	})
}

// handleUninstallPlugin 卸载运行时 Web 插件，保留数据目录。
func (k *Kernel) handleUninstallPlugin(c *fiber.Ctx) error {
	id := strings.TrimSpace(c.Params("id"))
	if k.builtinIDs[id] {
		return fiber.NewError(fiber.StatusForbidden, "内置插件不可卸载")
	}
	wa := k.installedApp(id)
	if wa == nil {
		return fiber.NewError(fiber.StatusNotFound, "插件未安装: "+id)
	}
	if !insideDir(k.webRoot, wa.dir) {
		return fiber.NewError(fiber.StatusBadRequest, "该插件来自外部插件目录，请在文件系统中移除")
	}

	trash := filepath.Join(k.webRoot, ".trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "无法创建回收目录: "+err.Error())
	}
	moved := filepath.Join(trash, fmt.Sprintf("%s-%d", id, time.Now().UnixNano()))
	if err := os.Rename(wa.dir, moved); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "卸载失败: "+err.Error())
	}

	k.webMu.Lock()
	delete(k.webApps, id)
	k.webMu.Unlock()
	_ = os.RemoveAll(moved)

	k.log.Info("已卸载 Web 插件", "id", id)
	return c.JSON(fiber.Map{
		"status":  "uninstalled",
		"id":      id,
		"dataDir": filepath.Join(k.dataDir, "apps", id),
		"message": "插件已卸载，数据目录已保留",
	})
}

// rewritePluginID 在安装副本时改写 plugin.json 中的 id。
func rewritePluginID(root, newID string) error {
	p := filepath.Join(root, "plugin.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	m["id"] = newID
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}

// ---------------------------------------------------------------------------
// ZIP 解压与结构校验
// ---------------------------------------------------------------------------

func saveUpload(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

// extractZip 解压 zip，防护 Zip Slip、符号链接与体积炸弹。
func extractZip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return errors.New("不是合法的 zip 文件")
	}
	defer r.Close()
	if len(r.File) > maxPluginFiles {
		return fmt.Errorf("zip 条目过多（上限 %d）", maxPluginFiles)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	var total int64
	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		// 部分打包工具（如 PowerShell Compress-Archive）不会为目录条目设置目录标记，
		// 但会保留结尾的斜杠，需要按名称判断。
		isDir := f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, "\\")
		if strings.Contains(name, "\x00") {
			return fmt.Errorf("非法文件名: %s", f.Name)
		}
		if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || strings.Contains(name, ":") {
			return fmt.Errorf("zip 包含绝对路径: %s", f.Name)
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("zip 包含越界路径: %s", f.Name)
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip 不允许包含符号链接: %s", f.Name)
		}
		if strings.HasPrefix(clean, "__MACOSX/") {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))
		if !insideDir(dest, target) {
			return fmt.Errorf("zip 包含越界路径: %s", f.Name)
		}
		if isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("创建目录 %q 失败（解压根 %q）: %w", target, dest, err)
			}
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > maxPluginUnzipped {
			return errors.New("解压后体积超过上限（256MB）")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", f.Name, err)
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// locatePluginRoot 定位插件根目录：zip 根目录或唯一顶层目录下必须有 plugin.json。
func locatePluginRoot(dir string) (string, error) {
	if st, err := os.Stat(filepath.Join(dir, "plugin.json")); err == nil && !st.IsDir() {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "__") {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		sub := filepath.Join(dir, dirs[0])
		if st, err := os.Stat(filepath.Join(sub, "plugin.json")); err == nil && !st.IsDir() {
			return sub, nil
		}
	}
	return "", errors.New("zip 结构不符合规范：根目录或唯一顶层目录下必须存在 plugin.json")
}

// insideDir 判断 target 是否位于 base 之内。
func insideDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ---------------------------------------------------------------------------
// 版本比较
// ---------------------------------------------------------------------------

// compareVersions 返回 newer（新包更新）、older（新包更旧）或 same。
func compareVersions(a, b string) string {
	switch compareVersionString(a, b) {
	case 1:
		return "newer"
	case -1:
		return "older"
	default:
		return "same"
	}
}

func compareVersionString(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var sa, sb string
		if i < len(pa) {
			sa = pa[i]
		}
		if i < len(pb) {
			sb = pb[i]
		}
		if c := compareVersionPart(sa, sb); c != 0 {
			return c
		}
	}
	return 0
}

func splitVersion(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if v == "" {
		return nil
	}
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	return strings.FieldsFunc(v, func(r rune) bool { return r == '.' })
}

func compareVersionPart(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	if ea == nil && eb == nil {
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		default:
			return 0
		}
	}
	if ea == nil && b == "" {
		if na == 0 {
			return 0
		}
		return 1
	}
	if eb == nil && a == "" {
		if nb == 0 {
			return 0
		}
		return -1
	}
	return strings.Compare(a, b)
}
