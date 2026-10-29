package kernel

import (
	"net/url"
	"runtime"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// WindowHost 提供对原生桌面窗口的控制能力。
// 未运行在原生窗口环境（例如回退为浏览器访问）时可用性为 false。
type WindowHost interface {
	Available() bool
	Minimise()
	ToggleMaximise() bool
	IsMaximised() bool
	Close()
}

// SetWindowHost 注入桌面窗口控制实现，供自绘标题栏调用。
func (k *Kernel) SetWindowHost(h WindowHost) {
	k.windowHost = h
}

// windowRoutes 实现自绘标题栏依赖的窗口接口。
type windowRoutes struct {
	host    func() WindowHost
	baseURL string
}

// RegisterWindowRoutes 在指定服务上注册标题栏接口，供主服务与初始化向导复用。
// host 以函数形式提供，便于窗口在服务启动之后才创建的场景。
func RegisterWindowRoutes(app *fiber.App, host func() WindowHost, baseURL string) {
	wr := &windowRoutes{host: host, baseURL: baseURL}
	app.Get("/api/window/state", wr.state)
	app.Post("/api/window/:action", wr.command)
}

// current 返回当前可用的窗口控制实现，未就绪时返回 nil。
func (w *windowRoutes) current() WindowHost {
	if w.host == nil {
		return nil
	}
	if h := w.host(); h != nil && h.Available() {
		return h
	}
	return nil
}

// state 返回窗口状态，前端据此渲染标题栏样式与按钮位置。
func (w *windowRoutes) state(c *fiber.Ctx) error {
	host := w.current()
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{
		"platform":  runtime.GOOS,
		"desktop":   host != nil,
		"maximised": host != nil && host.IsMaximised(),
	})
}

// command 执行标题栏的窗口操作：最小化、最大化切换、关闭。
func (w *windowRoutes) command(c *fiber.Ctx) error {
	host := w.current()
	if host == nil {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "当前环境不支持窗口操作"})
	}
	if err := verifyRequestOrigin(c, w.baseURL); err != nil {
		return err
	}
	payload := fiber.Map{"maximised": host.IsMaximised()}
	switch c.Params("action") {
	case "minimize":
		host.Minimise()
	case "maximize":
		payload["maximised"] = host.ToggleMaximise()
	case "close":
		host.Close()
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "未知的窗口操作"})
	}
	return c.JSON(payload)
}

// verifyRequestOrigin 校验写操作来自同源的 DevOS 桌面，降低 CSRF 风险。
func verifyRequestOrigin(c *fiber.Ctx, baseURL string) error {
	if c.Get("X-DevOS-Request") != "1" {
		return fiber.NewError(fiber.StatusBadRequest, "缺少请求标识，请从 DevOS 桌面操作")
	}
	origin := strings.TrimSpace(c.Get("Origin"))
	if origin == "" {
		return nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return fiber.NewError(fiber.StatusForbidden, "请求来源不受信任")
	}
	parsed, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
		return fiber.NewError(fiber.StatusForbidden, "请求来源不受信任")
	}
	return nil
}
