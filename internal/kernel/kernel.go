// Package kernel 是 DevOS 的内核：负责插件装载、HTTP 服务与静态资源编排。
package kernel

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"

	"devos/internal/plugin"
	"devos/internal/security"
)

// Version 是应用版本号，发布构建时通过 -ldflags "-X devos/internal/kernel.Version=..." 覆盖。
var Version = "0.1.0"

// Kernel 持有内核运行时状态。
type Kernel struct {
	app     *fiber.App
	plugins []plugin.Plugin
	dataDir string
	baseURL string
	log     *slog.Logger
	webFS   fs.FS

	sec          *security.Store
	sessions     *security.Sessions
	throttle     *security.Throttle
	wallpaperDir string

	webRoot    string             // 运行时 Web 插件安装目录
	builtinIDs map[string]bool    // 内置插件 ID，禁止被 Web 插件覆盖
	webMu      sync.RWMutex       // 保护 webApps
	webApps    map[string]*webApp // 已装载的 Web 插件
}

func New(webFS fs.FS, dataDir, baseURL string, log *slog.Logger) *Kernel {
	return &Kernel{
		app: fiber.New(fiber.Config{
			AppName:               "DevOS",
			DisableStartupMessage: true,
			BodyLimit:             64 * 1024 * 1024,
			ReadTimeout:           0,
			WriteTimeout:          0,
			ErrorHandler:          errorHandler,
		}),
		dataDir: dataDir,
		baseURL: baseURL,
		log:     log,
		webFS:   webFS,
		webRoot: filepath.Join(dataDir, "plugins"),
		webApps: map[string]*webApp{},
	}
}

// Load 初始化并注册全部插件（内置 + 运行时 Web 插件）。
func (k *Kernel) Load(builtin []plugin.Plugin) error {
	if err := k.initSecurity(); err != nil {
		return err
	}
	seen := map[string]bool{}
	k.builtinIDs = make(map[string]bool, len(builtin))
	for _, p := range builtin {
		m := p.Manifest()
		if m.ID == "" {
			return fmt.Errorf("插件缺少 id: %s", m.Name)
		}
		if seen[m.ID] {
			k.log.Warn("跳过重复的插件", "id", m.ID)
			continue
		}
		seen[m.ID] = true
		k.builtinIDs[m.ID] = true
		k.plugins = append(k.plugins, p)

		appDir := filepath.Join(k.dataDir, "apps", m.ID)
		if err := os.MkdirAll(appDir, 0o755); err != nil {
			return err
		}
		ctx := &plugin.Context{
			DataDir: appDir,
			RootDir: k.dataDir,
			Logger:  k.log.With("app", m.ID),
			BaseURL: k.baseURL,
		}
		if err := p.Init(ctx); err != nil {
			return fmt.Errorf("初始化插件 %s 失败: %w", m.ID, err)
		}
	}

	// 运行时 Web 插件来自三个位置：用户数据目录（可安装）、可执行文件同级、当前工作目录。
	if err := os.MkdirAll(k.webRoot, 0o755); err != nil {
		return err
	}
	candidates := []string{k.webRoot}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "plugins"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "plugins"))
	}
	seenDir := map[string]bool{}
	for _, dir := range candidates {
		abs, err := filepath.Abs(dir)
		if err != nil || seenDir[abs] {
			continue
		}
		seenDir[abs] = true
		k.scanWebPlugins(abs)
	}

	k.routes()
	return nil
}

func (k *Kernel) routes() {
	app := k.app

	// 应用锁：必须在所有业务路由之前注册，锁定状态下拦截 API 与应用页面。
	app.Use(k.securityGuard)

	app.Get("/api/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"app": "devos", "version": Version, "pid": os.Getpid()})
	})

	k.registerSecurityRoutes(app)

	app.Get("/api/system", func(c *fiber.Ctx) error {
		host, _ := os.Hostname()
		return c.JSON(fiber.Map{
			"hostname": host,
			"os":       osName(),
			"baseURL":  k.baseURL,
			"dataDir":  k.dataDir,
			"apps":     k.appCount(),
			"uptime":   int(time.Since(startedAt).Seconds()),
		})
	})

	app.Get("/api/apps", func(c *fiber.Ctx) error {
		return c.JSON(k.manifestList())
	})

	// 插件管理 API（内置插件与运行时 Web 插件统一视图）。
	app.Get("/api/plugins", k.handleListPlugins)
	app.Post("/api/plugins/install", k.handleInstallPlugin)
	app.Delete("/api/plugins/:id", k.handleUninstallPlugin)

	for _, p := range k.plugins {
		m := p.Manifest()
		if m.Entry == "" {
			m.Entry = "index.html"
		}

		api := app.Group("/api/apps/" + m.ID)
		p.Register(api)

		assets := p.Assets()
		if assets != nil {
			// 插件随内核重新编译，禁止浏览器复用旧静态资源。
			app.Use("/apps/"+m.ID, func(c *fiber.Ctx) error {
				c.Set("Cache-Control", "no-cache")
				return c.Next()
			})
			app.Use("/apps/"+m.ID, filesystem.New(filesystem.Config{
				Root:  http.FS(assets),
				Index: "index.html",
			}))
		}
	}

	for _, p := range k.plugins {
		if pub, ok := p.(plugin.PublicPlugin); ok {
			pub.RegisterPublic(app.Group("/mock"))
		}
	}

	// 运行时 Web 插件（动态注册表，支持热安装）。
	app.Get("/apps/:appId", k.serveWebApp)
	app.Get("/apps/:appId/*", k.serveWebApp)

	// 桌面前端静态资源（放在最后，避免覆盖 API 路由）。
	app.Use("/", filesystem.New(filesystem.Config{
		Root:  http.FS(k.webFS),
		Index: "index.html",
	}))
}

// Apps 返回已装载插件的清单，用于托盘菜单与外部展示。
func (k *Kernel) Apps() []plugin.Manifest {
	return k.manifestList()
}

// Start 在给定监听器上提供服务，并阻塞直到停止。
func (k *Kernel) Start(ln net.Listener) error {
	k.log.Info("DevOS 已启动", "url", k.baseURL)
	err := k.app.Listener(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (k *Kernel) Shutdown() error {
	return k.app.Shutdown()
}

var startedAt = time.Now()

// errorHandler 统一错误响应：API 返回 JSON，页面请求返回纯文本。
func errorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := err.Error()
	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
		msg = fe.Message
	}
	if strings.HasPrefix(c.Path(), "/api/") {
		return c.Status(code).JSON(fiber.Map{"error": msg, "status": code})
	}
	return c.Status(code).SendString(msg)
}

func osName() string {
	return runtime.GOOS
}
