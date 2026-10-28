package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"devos/internal/config"
	"devos/internal/datadir"
	"devos/internal/kernel"
	"devos/internal/platform"
)

// resolveDataDir 读取已保存的数据目录。
// 若不存在选择记录，则返回默认目录并提示需要进入初始化向导；
// 旧版本安装（默认目录已有数据）会自动写入选择记录，避免重复引导。
func resolveDataDir(forceSetup bool) (string, bool, error) {
	root, err := datadir.ConfigRoot()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false, err
	}
	if !forceSetup {
		locFile, err := datadir.LocationPath()
		if err != nil {
			return "", false, err
		}
		if data, err := os.ReadFile(locFile); err == nil {
			var loc struct {
				DataDir string `json:"dataDir"`
			}
			if json.Unmarshal(data, &loc) == nil && strings.TrimSpace(loc.DataDir) != "" {
				return loc.DataDir, false, nil
			}
		}
		if hasExistingData(root) {
			_ = datadir.WriteLocation(root)
			return root, false, nil
		}
	}
	return root, true, nil
}

// hasExistingData 判断目录中是否已有 DevOS 数据，用于兼容旧版本安装。
func hasExistingData(dir string) bool {
	for _, name := range []string{"config.json", "security.json"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// setupErrorPayload 返回带错误码的响应，便于前端本地化提示。
func setupErrorPayload(c *fiber.Ctx, status int, code string, err error) error {
	return c.Status(status).JSON(fiber.Map{"error": err.Error(), "code": code})
}

func setupError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, datadir.ErrEmpty):
		return setupErrorPayload(c, fiber.StatusBadRequest, "empty", err)
	case errors.Is(err, datadir.ErrNotAbs):
		return setupErrorPayload(c, fiber.StatusBadRequest, "not_abs", err)
	case errors.Is(err, datadir.ErrVolumeRoot):
		return setupErrorPayload(c, fiber.StatusBadRequest, "volume_root", err)
	case errors.Is(err, datadir.ErrMkdir):
		return setupErrorPayload(c, fiber.StatusBadRequest, "mkdir_failed", err)
	case errors.Is(err, datadir.ErrNotWritable):
		return setupErrorPayload(c, fiber.StatusBadRequest, "not_writable", err)
	default:
		return setupErrorPayload(c, fiber.StatusBadRequest, "invalid", err)
	}
}

// verifySetupRequest 校验请求来自本机 DevOS 初始化页面。
func verifySetupRequest(c *fiber.Ctx, baseURL string) error {
	if c.Get("X-DevOS-Request") != "1" {
		return fiber.NewError(fiber.StatusBadRequest, "缺少请求标识")
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

type setupOptions struct {
	BaseURL    string
	DefaultDir string
	OnReady    func(dataDir string)
}

// newSetupApp 构造首次启动的初始化向导服务。
func newSetupApp(webFS fs.FS, opts setupOptions) (*fiber.App, error) {
	page, err := fs.ReadFile(webFS, "setup.html")
	if err != nil {
		return nil, fmt.Errorf("读取初始化页面失败: %w", err)
	}
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			message := err.Error()
			var fe *fiber.Error
			if errors.As(err, &fe) {
				code = fe.Code
				message = fe.Message
			}
			return c.Status(code).JSON(fiber.Map{"error": message})
		},
	})
	app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store")
		c.Type("html", "utf-8")
		return c.Send(page)
	})
	app.Get("/favicon.ico", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	api := app.Group("/api/setup")
	api.Get("/status", func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store")
		return c.JSON(fiber.Map{
			"dataDir":  opts.DefaultDir,
			"platform": runtime.GOOS,
		})
	})
	api.Post("/browse", func(c *fiber.Ctx) error {
		if err := verifySetupRequest(c, opts.BaseURL); err != nil {
			return err
		}
		dir, err := platform.BrowseForFolder(opts.DefaultDir)
		if err != nil {
			return setupErrorPayload(c, fiber.StatusNotImplemented, "browse_unsupported", err)
		}
		return c.JSON(fiber.Map{"dir": dir})
	})
	api.Post("/choose", func(c *fiber.Ctx) error {
		if err := verifySetupRequest(c, opts.BaseURL); err != nil {
			return err
		}
		var in struct {
			Dir string `json:"dir"`
		}
		if err := c.BodyParser(&in); err != nil {
			return setupErrorPayload(c, fiber.StatusBadRequest, "parse_failed", errors.New("参数解析失败"))
		}
		dir, err := datadir.Normalize(in.Dir)
		if err != nil {
			return setupError(c, err)
		}
		if err := datadir.WriteLocation(dir); err != nil {
			return setupErrorPayload(c, fiber.StatusInternalServerError, "save_failed", err)
		}
		if opts.OnReady != nil {
			go opts.OnReady(dir)
		}
		return c.JSON(fiber.Map{"ok": true, "dataDir": dir, "url": opts.BaseURL})
	})
	return app, nil
}

// runSetupMode 运行初始化向导：选择目录后关闭向导服务，由主流程初始化数据。
// 返回选择的数据目录、向导占用的端口以及是否已自动打开浏览器。
func runSetupMode(cfg config.Config, logger *slog.Logger, defaultDir string) (string, int, bool, bool) {
	if url, ok := kernel.FindDevOS(cfg.Port, cfg.MaxPortTries); ok {
		logger.Info("DevOS 已在运行，打开已有实例", "url", url)
		if cfg.OpenBrowser {
			_ = kernel.OpenBrowser(url)
		}
		return "", 0, false, false
	}

	ln, port, err := kernel.Listen(cfg.Port, cfg.MaxPortTries)
	if err != nil {
		logger.Error("启动初始化向导失败", "err", err)
		os.Exit(1)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	webFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Error("加载初始化页面失败", "err", err)
		os.Exit(1)
	}
	chosen := make(chan string, 1)
	var app *fiber.App
	app, err = newSetupApp(webFS, setupOptions{
		BaseURL:    baseURL,
		DefaultDir: defaultDir,
		OnReady: func(dir string) {
			select {
			case chosen <- dir:
			default:
			}
			// 让响应先返回，再关闭向导服务释放端口。
			go func() {
				time.Sleep(300 * time.Millisecond)
				_ = app.Shutdown()
			}()
		},
	})
	if err != nil {
		logger.Error("初始化向导启动失败", "err", err)
		os.Exit(1)
	}

	logger.Info("等待选择数据目录", "url", baseURL, "default", defaultDir, "port", port)
	if cfg.OpenBrowser {
		go func() {
			time.Sleep(600 * time.Millisecond)
			_ = kernel.OpenBrowser(baseURL)
		}()
	}
	if err := app.Listener(ln); err != nil {
		logger.Error("初始化向导异常退出", "err", err)
		os.Exit(1)
	}

	select {
	case dir := <-chosen:
		logger.Info("数据目录已选择，开始初始化数据", "dir", dir)
		return dir, port, cfg.OpenBrowser, true
	default:
		return "", 0, cfg.OpenBrowser, false
	}
}
