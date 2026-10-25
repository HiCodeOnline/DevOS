package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"devos/internal/config"
	"devos/internal/kernel"
	"devos/internal/plugins"
)

//go:embed web
var webAssets embed.FS

func main() {
	portFlag := flag.Int("port", 0, "指定监听端口（默认读取 config.json，7788）")
	noTray := flag.Bool("no-tray", false, "不创建任务栏托盘图标")
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开浏览器")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dataDir, err := defaultDataDir()
	if err != nil {
		logger.Error("无法确定数据目录", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		logger.Error("无法创建数据目录", "dir", dataDir, "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		logger.Warn("读取配置失败，使用默认配置", "err", err)
		cfg = config.Default()
	}
	if *portFlag > 0 {
		cfg.Port = *portFlag
	}
	if *noBrowser {
		cfg.OpenBrowser = false
	}

	// 单实例：已有 DevOS 在运行则直接唤起桌面。
	if url, ok := kernel.FindDevOS(cfg.Port, cfg.MaxPortTries); ok {
		logger.Info("DevOS 已在运行，打开已有实例", "url", url)
		if cfg.OpenBrowser {
			_ = kernel.OpenBrowser(url)
		}
		return
	}

	ln, port, err := kernel.Listen(cfg.Port, cfg.MaxPortTries)
	if err != nil {
		logger.Error("启动失败", "err", err)
		os.Exit(1)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	webFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		logger.Error("加载桌面资源失败", "err", err)
		os.Exit(1)
	}

	k := kernel.New(webFS, dataDir, baseURL, logger)
	if err := k.Load(plugins.All()); err != nil {
		logger.Error("装载插件失败", "err", err)
		os.Exit(1)
	}

	go func() {
		if err := k.Start(ln); err != nil {
			logger.Error("服务异常退出", "err", err)
			os.Exit(1)
		}
	}()

	logger.Info("DevOS 已就绪", "url", baseURL, "data", dataDir, "port", port)

	if *noTray {
		if cfg.OpenBrowser && kernel.WaitReady(baseURL, 3*time.Second) {
			_ = kernel.OpenBrowser(baseURL)
		}
		kernel.WaitSignal(func() { _ = k.Shutdown() })
		return
	}

	trayApps := make([]kernel.TrayApp, 0, 8)
	for _, app := range k.Apps() {
		trayApps = append(trayApps, kernel.TrayApp{ID: app.ID, Name: app.Name})
	}

	kernel.RunTray(baseURL, dataDir, cfg.OpenBrowser, trayApps, func() { _ = k.Shutdown() })
}

func defaultDataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DevOS"), nil
}
