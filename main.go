package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"

	"devos/internal/config"
	"devos/internal/datadir"
	"devos/internal/desktop"
	"devos/internal/kernel"
	"devos/internal/plugins"
)

//go:embed web
var webAssets embed.FS

// runtimeState 保存内核、数据目录与桌面窗口，供托盘与标题栏回调并发访问。
type runtimeState struct {
	mu      sync.Mutex
	kernel  *kernel.Kernel
	dataDir string
	desk    *desktop.Desktop
}

func (s *runtimeState) setKernel(k *kernel.Kernel) {
	s.mu.Lock()
	s.kernel = k
	s.mu.Unlock()
}

func (s *runtimeState) setDesktop(d *desktop.Desktop) {
	s.mu.Lock()
	s.desk = d
	s.mu.Unlock()
}

// desktop 返回已创建的桌面窗口，尚未创建时返回 nil。
func (s *runtimeState) desktop() *desktop.Desktop {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desk
}

// windowHost 返回供内核注册标题栏接口的窗口控制对象。
func (s *runtimeState) windowHost() kernel.WindowHost { return s }

// Available 判断是否运行在支持窗口操作的原生桌面窗口中。
func (s *runtimeState) Available() bool {
	d := s.desktop()
	return d != nil && d.Available()
}

// Minimise 最小化主窗口。
func (s *runtimeState) Minimise() {
	if d := s.desktop(); d != nil {
		d.Minimise()
	}
}

// ToggleMaximise 切换最大化并返回切换后的状态。
func (s *runtimeState) ToggleMaximise() bool {
	if d := s.desktop(); d != nil {
		return d.ToggleMaximise()
	}
	return false
}

// IsMaximised 返回主窗口是否最大化。
func (s *runtimeState) IsMaximised() bool {
	if d := s.desktop(); d != nil {
		return d.IsMaximised()
	}
	return false
}

// Close 关闭主窗口；启用托盘时窗口仅隐藏，服务继续运行。
func (s *runtimeState) Close() {
	if d := s.desktop(); d != nil {
		d.Close()
	}
}

func (s *runtimeState) setDataDir(dir string) {
	s.mu.Lock()
	s.dataDir = dir
	s.mu.Unlock()
}

func (s *runtimeState) DataDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dataDir
}

// stopKernel 停止内核服务，供桌面应用退出前调用。
func (s *runtimeState) stopKernel() {
	s.mu.Lock()
	k := s.kernel
	s.mu.Unlock()
	if k != nil {
		_ = k.Shutdown()
	}
}

func main() {
	portFlag := flag.Int("port", 0, "指定监听端口（默认读取 config.json，7788）")
	noTray := flag.Bool("no-tray", false, "不创建任务栏托盘图标")
	noWindow := flag.Bool("no-browser", false, "启动后不自动显示主窗口（仅驻留托盘）")
	setupFlag := flag.Bool("setup", false, "强制显示数据目录初始化向导")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dataDir, needSetup, err := resolveDataDir(*setupFlag)
	if err != nil {
		logger.Error("无法确定数据目录", "err", err)
		os.Exit(1)
	}

	state := &runtimeState{dataDir: dataDir}

	if needSetup {
		runSetupWindow(state, logger, dataDir, *portFlag, *noTray, *noWindow)
		return
	}

	dir, err := prepareDataDir(logger, dataDir)
	if err != nil {
		logger.Error("无法创建数据目录", "dir", dataDir, "err", err)
		os.Exit(1)
	}
	state.setDataDir(dir)

	cfg := loadConfig(logger, dir, *portFlag, 0)
	if url, ok := kernel.FindDevOS(cfg.Port, cfg.MaxPortTries); ok {
		// 已有实例：本进程仅负责唤起它的窗口，随后由单实例机制退出。
		logger.Info("DevOS 已在运行，唤起已有窗口", "url", url)
		activateExisting(logger, url)
		return
	}

	k, baseURL, err := startServer(cfg, dir, logger, state.windowHost())
	if err != nil {
		logger.Error("启动失败", "err", err)
		os.Exit(1)
	}
	state.setKernel(k)

	desk := desktop.New(desktop.Options{
		Title:      "DevOS · 开发者工作台",
		Icon:       kernel.TrayIcon(),
		Logger:     logger,
		InitialURL: baseURL,
		Show:       cfg.OpenBrowser && !*noWindow,
		Tray:       !*noTray,
		DataDir:    state.DataDir,
		OnQuit:     state.stopKernel,
	})
	state.setDesktop(desk)
	desk.SetTrayApps(trayApps(k))

	if err := desk.Run(); err != nil {
		logger.Error("桌面应用异常退出", "err", err)
		os.Exit(1)
	}
}

// runSetupWindow 在原生窗口中显示数据目录初始化向导，确认后继续启动正式服务。
func runSetupWindow(state *runtimeState, logger *slog.Logger, defaultDir string, portFlag int, noTray, noWindow bool) {
	cfg := config.Default()
	if portFlag > 0 {
		cfg.Port = portFlag
	}
	if url, ok := kernel.FindDevOS(cfg.Port, cfg.MaxPortTries); ok {
		logger.Info("DevOS 已在运行，唤起已有窗口", "url", url)
		activateExisting(logger, url)
		return
	}

	wizardURL, setupPort, stopWizard, chosen := startSetupWizard(cfg, logger, defaultDir, state.windowHost)
	defer stopWizard()

	desk := desktop.New(desktop.Options{
		Title:      "DevOS · 数据目录初始化",
		Icon:       kernel.TrayIcon(),
		Logger:     logger,
		InitialURL: wizardURL,
		Show:       !noWindow,
		Tray:       !noTray,
		DataDir:    state.DataDir,
		OnQuit:     state.stopKernel,
	})
	state.setDesktop(desk)

	// 用户确认目录后，在同一窗口内继续启动正式服务。
	go func() {
		dir, ok := <-chosen
		stopWizard()
		if !ok {
			desk.Quit()
			return
		}
		prepared, err := prepareDataDir(logger, dir)
		if err != nil {
			logger.Error("无法创建数据目录", "dir", dir, "err", err)
			desk.Quit()
			return
		}
		state.setDataDir(prepared)
		k, baseURL, err := startServer(loadConfig(logger, prepared, portFlag, setupPort), prepared, logger, state.windowHost())
		if err != nil {
			logger.Error("启动失败", "err", err)
			desk.Quit()
			return
		}
		state.setKernel(k)
		desk.Open(baseURL)
		desk.SetTitle("DevOS · 开发者工作台")
		desk.SetTrayApps(trayApps(k))
	}()

	if err := desk.Run(); err != nil {
		logger.Error("桌面应用异常退出", "err", err)
		os.Exit(1)
	}
}

// activateExisting 创建桌面应用并加载已有实例的地址。
// 单实例机制会通知已有实例唤起窗口，本进程随即退出。
func activateExisting(logger *slog.Logger, url string) {
	desk := desktop.New(desktop.Options{
		Title:      "DevOS · 开发者工作台",
		Icon:       kernel.TrayIcon(),
		Logger:     logger,
		InitialURL: url,
		Show:       true,
		Tray:       false,
	})
	_ = desk.Run()
}

// prepareDataDir 创建数据目录并执行设置页排入的还原/迁移任务。
func prepareDataDir(logger *slog.Logger, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir, err
	}
	return datadir.ApplyPending(logger, dir), nil
}

// loadConfig 读取数据目录中的配置；portFlag 优先，其次复用初始化向导端口。
func loadConfig(logger *slog.Logger, dataDir string, portFlag, setupPort int) config.Config {
	cfg, err := config.Load(dataDir)
	if err != nil {
		logger.Warn("读取配置失败，使用默认配置", "err", err)
		cfg = config.Default()
	}
	if portFlag > 0 {
		cfg.Port = portFlag
	} else if setupPort > 0 {
		// 复用初始化向导的端口，让向导页面能自动跳转到正式服务。
		cfg.Port = setupPort
	}
	return cfg
}

// startServer 启动内核服务并返回内核实例与访问地址。
func startServer(cfg config.Config, dataDir string, logger *slog.Logger, host kernel.WindowHost) (*kernel.Kernel, string, error) {
	ln, port, err := kernel.Listen(cfg.Port, cfg.MaxPortTries)
	if err != nil {
		return nil, "", fmt.Errorf("监听端口失败: %w", err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	webFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("加载桌面资源失败: %w", err)
	}

	k := kernel.New(webFS, dataDir, baseURL, logger)
	k.SetWindowHost(host)
	if err := k.Load(plugins.All()); err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("装载插件失败: %w", err)
	}

	go func() {
		if err := k.Start(ln); err != nil {
			logger.Error("服务异常退出", "err", err)
		}
	}()

	logger.Info("DevOS 已就绪", "url", baseURL, "data", dataDir, "port", port)
	return k, baseURL, nil
}

// trayApps 返回托盘菜单中的快捷应用列表。
func trayApps(k *kernel.Kernel) []desktop.AppInfo {
	apps := k.Apps()
	result := make([]desktop.AppInfo, 0, len(apps))
	for _, app := range apps {
		result = append(result, desktop.AppInfo{ID: app.ID, Name: app.Name})
	}
	return result
}
