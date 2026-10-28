//go:build windows

package desktop

import (
	"net/url"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"devos/internal/kernel"
)

// Desktop 使用 Wails v3 承载原生窗口与托盘。
type Desktop struct {
	opts     Options
	app      *application.App
	win      *application.WebviewWindow
	tray     *application.SystemTray
	baseURL  string
	apps     []AppInfo
	quitting atomic.Bool
}

// New 创建桌面应用：窗口与托盘在此创建，随后由 Run 启动消息循环。
func New(opts Options) *Desktop {
	d := &Desktop{opts: opts, baseURL: opts.InitialURL}

	// 使用托盘时关闭窗口只隐藏，否则关闭最后一个窗口即退出。
	windowsOptions := application.WindowsOptions{
		DisableQuitOnLastWindowClosed: opts.Tray,
	}

	d.app = application.New(application.Options{
		Name:        "DevOS",
		Description: "DevOS 开发者工作台",
		Icon:        opts.Icon,
		Logger:      opts.Logger,
		Assets:      application.AssetOptions{DisableLogging: true},
		// 单实例：再次启动时由已有实例唤起窗口，新进程直接退出。
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "devos.desktop.workbench",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				go d.Show()
			},
		},
		OnShutdown: func() {
			if opts.OnQuit != nil {
				opts.OnQuit()
			}
		},
		Windows: windowsOptions,
	})

	title := opts.Title
	if title == "" {
		title = "DevOS"
	}
	d.win = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            title,
		Width:            1360,
		Height:           860,
		MinWidth:         1024,
		MinHeight:        660,
		InitialPosition:  application.WindowCentered,
		URL:              opts.InitialURL,
		Hidden:           !opts.Show,
		BackgroundColour: application.NewRGB(16, 18, 23),
		Windows: application.WindowsWindow{
			DisableMenu: true,
			Theme:       application.SystemDefault,
		},
	})

	if opts.Tray {
		d.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			if d.quitting.Load() {
				return
			}
			// 收起窗口，保持后台服务运行。
			d.win.Hide()
			e.Cancel()
		})
		d.tray = d.app.SystemTray.New()
		d.tray.SetIcon(opts.Icon)
		d.tray.SetTooltip("DevOS · 开发者工作台")
		d.tray.OnDoubleClick(d.Show)
		d.refreshMenu()
	}

	return d
}

// Run 启动消息循环并阻塞直到应用退出。
func (d *Desktop) Run() error {
	return d.app.Run()
}

// Show 显示并聚焦主窗口。
func (d *Desktop) Show() {
	if d.win == nil {
		return
	}
	d.win.Show().Focus()
}

// Open 在主窗口中重新加载指定地址并显示窗口。
func (d *Desktop) Open(baseURL string) {
	if baseURL == "" {
		d.Show()
		return
	}
	d.baseURL = baseURL
	d.win.SetURL(baseURL)
	d.Show()
}

// SetTitle 更新主窗口标题。
func (d *Desktop) SetTitle(title string) {
	if d.win != nil && title != "" {
		d.win.SetTitle(title)
	}
}

// OpenApp 在主窗口中打开指定应用页面。
func (d *Desktop) OpenApp(id string) {
	if d.baseURL == "" || id == "" {
		d.Show()
		return
	}
	target := d.baseURL + "/?app=" + url.QueryEscape(id)
	d.win.SetURL(target)
	d.Show()
}

// SetTrayApps 更新托盘中的快捷应用列表。
func (d *Desktop) SetTrayApps(apps []AppInfo) {
	d.apps = append([]AppInfo(nil), apps...)
	if d.tray != nil {
		d.refreshMenu()
	}
}

// refreshMenu 重建托盘菜单。
func (d *Desktop) refreshMenu() {
	menu := d.app.NewMenu()
	menu.Add("打开 DevOS 桌面").OnClick(func(*application.Context) { d.Show() })
	for _, app := range d.apps {
		id := app.ID
		menu.Add(app.Name).OnClick(func(*application.Context) { d.OpenApp(id) })
	}
	menu.AddSeparator()
	menu.Add("打开数据目录").OnClick(func(*application.Context) {
		if d.opts.DataDir != nil {
			_ = kernel.OpenPath(d.opts.DataDir())
		}
	})
	menu.AddSeparator()
	menu.Add("退出 DevOS").OnClick(func(*application.Context) { d.Quit() })
	d.tray.SetMenu(menu)
}

// Quit 退出桌面应用，OnShutdown 中会停止后台服务。
func (d *Desktop) Quit() {
	d.quitting.Store(true)
	if d.app != nil {
		d.app.Quit()
	}
}
