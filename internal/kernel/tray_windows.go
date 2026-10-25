//go:build windows

package kernel

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/energye/systray"
)

// TrayApp 是托盘菜单里可快速打开的应用。
type TrayApp struct {
	ID   string
	Name string
}

// RunTray 在 Windows 任务栏通知区创建图标并阻塞运行。
func RunTray(baseURL, dataDir string, openBrowser bool, apps []TrayApp, onQuit func()) {
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		onQuit()
		systray.Quit()
	}()

	systray.Run(func() {
		systray.SetIcon(TrayIcon())
		systray.SetTooltip("DevOS · 开发者工作台")
		systray.SetOnDClick(func(systray.IMenu) { _ = OpenBrowser(baseURL) })

		mOpen := systray.AddMenuItem("打开 DevOS 桌面", "在浏览器中打开桌面")
		for _, app := range apps {
			item := systray.AddMenuItem(app.Name, "打开 "+app.Name)
			id := app.ID
			item.Click(func() {
				_ = OpenBrowser(baseURL + "/?app=" + id)
			})
		}
		systray.AddSeparator()
		mDir := systray.AddMenuItem("打开数据目录", dataDir)
		mQuit := systray.AddMenuItem("退出 DevOS", "停止服务并退出")

		mOpen.Click(func() { _ = OpenBrowser(baseURL) })
		mDir.Click(func() { _ = OpenPath(dataDir) })
		mQuit.Click(func() {
			onQuit()
			systray.Quit()
		})

		if openBrowser && WaitReady(baseURL, 3*time.Second) {
			_ = OpenBrowser(baseURL)
		}
	}, func() {})
}
