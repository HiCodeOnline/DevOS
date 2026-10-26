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
	"devos/internal/kernel"
)

// dataLocation 记录用户选择的数据存储目录，固定保存在用户配置目录下。
type dataLocation struct {
	DataDir string `json:"dataDir"`
	SavedAt string `json:"savedAt"`
}

// configRootDir 返回固定的配置目录（默认数据目录），用于存放目录选择结果。
func configRootDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DevOS"), nil
}

// resolveDataDir 读取已保存的数据目录。
// 若不存在选择记录，则返回默认目录并提示需要进入初始化向导；
// 旧版本安装（默认目录已有数据）会自动写入选择记录，避免重复引导。
func resolveDataDir(forceSetup bool) (string, bool, error) {
	root, err := configRootDir()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false, err
	}
	locFile := filepath.Join(root, "location.json")
	if !forceSetup {
		if data, err := os.ReadFile(locFile); err == nil {
			var loc dataLocation
			if json.Unmarshal(data, &loc) == nil && strings.TrimSpace(loc.DataDir) != "" {
				return loc.DataDir, false, nil
			}
		}
		if hasExistingData(root) {
			_ = writeLocation(locFile, root)
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

func writeLocation(path, dir string) error {
	payload, err := json.MarshalIndent(dataLocation{
		DataDir: dir,
		SavedAt: time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// normalizeDataDir 校验并创建用户选择的数据目录。
func normalizeDataDir(input string) (string, error) {
	dir := strings.TrimSpace(input)
	if dir == "" {
		return "", errSetupEmpty
	}
	if !filepath.IsAbs(dir) {
		return "", errSetupNotAbs
	}
	clean := filepath.Clean(dir)
	volume := filepath.VolumeName(clean)
	if volume != "" && (clean == volume || clean == volume+string(filepath.Separator)) {
		return "", errSetupVolumeRoot
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return "", fmt.Errorf("%w: %v", errSetupMkdir, err)
	}
	probe := filepath.Join(clean, ".devos-write-probe")
	if err := os.WriteFile(probe, []byte("devos"), 0o600); err != nil {
		return "", errSetupNotWritable
	}
	_ = os.Remove(probe)
	return clean, nil
}

var (
	errSetupEmpty       = errors.New("请输入数据目录")
	errSetupNotAbs      = errors.New("数据目录必须为绝对路径")
	errSetupVolumeRoot  = errors.New("不能直接使用磁盘根目录")
	errSetupMkdir       = errors.New("无法创建数据目录")
	errSetupNotWritable = errors.New("数据目录不可写")
)

// setupErrorPayload 返回带错误码的响应，便于前端本地化提示。
func setupErrorPayload(c *fiber.Ctx, status int, code string, err error) error {
	return c.Status(status).JSON(fiber.Map{"error": err.Error(), "code": code})
}

func setupError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, errSetupEmpty):
		return setupErrorPayload(c, fiber.StatusBadRequest, "empty", err)
	case errors.Is(err, errSetupNotAbs):
		return setupErrorPayload(c, fiber.StatusBadRequest, "not_abs", err)
	case errors.Is(err, errSetupVolumeRoot):
		return setupErrorPayload(c, fiber.StatusBadRequest, "volume_root", err)
	case errors.Is(err, errSetupMkdir):
		return setupErrorPayload(c, fiber.StatusBadRequest, "mkdir_failed", err)
	case errors.Is(err, errSetupNotWritable):
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
	BaseURL      string
	DefaultDir   string
	LocationFile string
	OnReady      func(dataDir string)
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
		dir, err := browseForFolder(opts.DefaultDir)
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
		dir, err := normalizeDataDir(in.Dir)
		if err != nil {
			return setupError(c, err)
		}
		if err := os.MkdirAll(filepath.Dir(opts.LocationFile), 0o755); err != nil {
			return setupErrorPayload(c, fiber.StatusInternalServerError, "save_failed", err)
		}
		if err := writeLocation(opts.LocationFile, dir); err != nil {
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
	rootDir, err := configRootDir()
	if err != nil {
		logger.Error("无法确定配置目录", "err", err)
		os.Exit(1)
	}

	chosen := make(chan string, 1)
	var app *fiber.App
	app, err = newSetupApp(webFS, setupOptions{
		BaseURL:      baseURL,
		DefaultDir:   defaultDir,
		LocationFile: filepath.Join(rootDir, "location.json"),
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
