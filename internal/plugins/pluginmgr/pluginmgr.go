// Package pluginmgr 提供插件管理应用：ZIP 安装、更新冲突处理与卸载。
package pluginmgr

import (
	"embed"
	"io/fs"

	"github.com/gofiber/fiber/v2"

	"devos/internal/plugin"
)

//go:embed web
var webFS embed.FS

type Manager struct{}

func New() *Manager { return &Manager{} }

func (p *Manager) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "plugins",
		Name:        "插件管理",
		Description: "离线安装 Web 插件、处理版本冲突与卸载",
		Version:     "1.0.0",
		Category:    "系统",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M9 4a2 2 0 0 1 4 0v2h3a2 2 0 0 1 2 2v3h-2a2 2 0 0 0 0 4h2v3a2 2 0 0 1-2 2h-3v-2a2 2 0 0 0-4 0v2H6a2 2 0 0 1-2-2v-3h2a2 2 0 0 0 0-4H4V8a2 2 0 0 1 2-2h3V4Z"/></svg>`,
		Accent:      "#6366f1",
		Entry:       "index.html",
		Width:       1080,
		Height:      720,
		Multi:       false,
		Builtin:     true,
		Backend:     false,
	}
}

func (p *Manager) Init(ctx *plugin.Context) error { return nil }

func (p *Manager) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *Manager) Register(api fiber.Router) {}
