// Package toolbox 提供常用开发工具箱应用（纯前端）。
package toolbox

import (
	"embed"
	"io/fs"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

//go:embed web
var webFS embed.FS

type Toolbox struct{}

func New() *Toolbox { return &Toolbox{} }

func (p *Toolbox) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "toolbox",
		Name:        "工具集",
		Description: "JSON、编码、时间戳、哈希等日常开发小工具",
		Version:     "0.1.0",
		Category:    "工具",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a4 4 0 0 0 5 5L21 10l-7.5 7.5-2.4 2.4a2 2 0 0 1-2.8 0l-1.2-1.2a2 2 0 0 1 0-2.8L9.5 13.5 17 6l-2.3-1.1Z"/><path d="M4 20l3-3"/></svg>`,
		Accent:      "#8b5cf6",
		Entry:       "index.html",
		Width:       1060,
		Height:      700,
		Multi:       false,
		Builtin:     true,
		Backend:     false,
	}
}

func (p *Toolbox) Init(ctx *plugin.Context) error { return nil }

func (p *Toolbox) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *Toolbox) Register(api fiber.Router) {}
