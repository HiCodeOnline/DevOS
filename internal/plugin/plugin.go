// Package plugin 定义 DevOS 的插件契约。
// 内置插件由 Go 包实现该契约；运行时 Web 插件由内核适配为同一契约。
package plugin

import (
	"io/fs"
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// Manifest 描述一个应用在桌面上展示所需的全部元信息。
type Manifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`   // 内联 SVG，或插件内图片路径（如 icon.png）
	Accent      string `json:"accent"` // 主题强调色，如 #0ea5e9
	Entry       string `json:"entry"`  // 静态资源根目录内的入口页面，默认 index.html
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Multi       bool   `json:"multi"`   // 是否允许打开多个窗口
	Builtin     bool   `json:"builtin"` // 是否随内核编译
	Backend     bool   `json:"backend"` // 是否提供后端 API

	// 以下字段用于运行时 Web 插件（见 docs/web-plugins.md）。
	SpecVersion int    `json:"specVersion,omitempty"` // 插件规范版本，当前为 1
	StaticRoot  string `json:"staticRoot,omitempty"`  // 静态资源目录，默认 web
	SPA         bool   `json:"spa,omitempty"`         // 无扩展名路径未命中时回退到入口页
}

// Context 是内核注入给插件的运行环境。
type Context struct {
	DataDir string // 该插件专属数据目录
	RootDir string // DevOS 全局数据目录（共享配置存放于此）
	Logger  *slog.Logger
	BaseURL string // 例如 http://127.0.0.1:7788
}

// Plugin 是内置插件必须实现的接口。
type Plugin interface {
	Manifest() Manifest
	Init(ctx *Context) error
	Register(api fiber.Router)
	Assets() fs.FS
}

// PublicPlugin 可选实现：允许插件在内核根路径挂载公开路由（如 Mock 服务）。
type PublicPlugin interface {
	RegisterPublic(router fiber.Router)
}
