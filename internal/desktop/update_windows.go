//go:build windows

package desktop

import (
	"context"
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"

	"devos/internal/kernel"
)

// 自动更新节奏：启动后延迟检查一次，之后按间隔轮询。
const (
	updateFirstCheckDelay = 30 * time.Second
	updateCheckInterval   = 12 * time.Hour
	updateRequestTimeout  = 5 * time.Minute
)

// startUpdater 配置 GitHub Release 自动更新。仅正式发布构建启用，
// 开发构建（-ldflags 未注入 BuildRelease）保持关闭，避免替换本地调试产物。
func (d *Desktop) startUpdater() {
	if !kernel.IsReleaseBuild() {
		return
	}
	logger := d.opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	provider, err := github.New(github.Config{
		Repository:    UpdateRepository,
		ChecksumAsset: UpdateChecksumAsset,
		AssetMatcher:  selectWindowsAsset,
	})
	if err != nil {
		logger.Error("初始化更新源失败", "err", err)
		return
	}

	err = d.app.Updater.Init(updater.Config{
		CurrentVersion: kernel.Version,
		Providers:      []updater.Provider{provider},
		// 内置更新窗口：检查到新版本后展示说明、下载进度与「重启并应用」。
		Window: &updater.BuiltinWindow{
			Options: updater.WindowOptions{Title: "DevOS 更新"},
		},
	})
	if err != nil {
		logger.Error("初始化自动更新失败", "err", err)
		return
	}

	// 点击「重启并应用」时先放行窗口关闭，否则托盘模式下会被拦截而无法退出。
	d.app.Event.On(updater.EventUserRestart, func(*application.CustomEvent) {
		d.quitting.Store(true)
	})

	go d.autoUpdate(logger)
}

// autoUpdate 周期检查新版本，发现后交给内置窗口下载并在用户确认后重启。
func (d *Desktop) autoUpdate(logger *slog.Logger) {
	timer := time.NewTimer(updateFirstCheckDelay)
	<-timer.C

	for {
		ctx, cancel := context.WithTimeout(context.Background(), updateRequestTimeout)
		release, err := d.app.Updater.Check(ctx)
		cancel()
		if err != nil {
			logger.Warn("检查更新失败", "err", err)
		} else if release != nil {
			logger.Info("发现新版本", "version", release.Version)
			// 打开更新窗口并下载；窗口内可取消，完成后由用户点击重启应用。
			if err := d.app.Updater.CheckAndInstall(context.Background()); err != nil {
				logger.Warn("更新流程中断", "err", err)
			}
		}
		time.Sleep(updateCheckInterval)
	}
}
