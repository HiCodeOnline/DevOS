//go:build !windows

package desktop

import "devos/internal/kernel"

// Desktop 在非 Windows 平台回退为使用系统浏览器访问。
type Desktop struct {
	opts    Options
	baseURL string
}

// New 创建桌面应用；非 Windows 平台仅打开默认浏览器。
func New(opts Options) *Desktop {
	d := &Desktop{opts: opts, baseURL: opts.InitialURL}
	if opts.Show && opts.InitialURL != "" {
		_ = kernel.OpenBrowser(opts.InitialURL)
	}
	return d
}

// Run 阻塞直到收到退出信号。
func (d *Desktop) Run() error {
	kernel.WaitSignal(func() {
		if d.opts.OnQuit != nil {
			d.opts.OnQuit()
		}
	})
	return nil
}

// Show 显示主窗口，即重新打开浏览器页面。
func (d *Desktop) Show() {
	if d.baseURL != "" {
		_ = kernel.OpenBrowser(d.baseURL)
	}
}

// Open 在浏览器中打开指定地址。
func (d *Desktop) Open(baseURL string) {
	if baseURL == "" {
		d.Show()
		return
	}
	d.baseURL = baseURL
	_ = kernel.OpenBrowser(baseURL)
}

// OpenApp 在浏览器中打开指定应用页面。
func (d *Desktop) OpenApp(id string) {
	if d.baseURL == "" || id == "" {
		d.Show()
		return
	}
	_ = kernel.OpenBrowser(d.baseURL + "/?app=" + id)
}

// SetTitle 在非 Windows 平台无需处理。
func (d *Desktop) SetTitle(string) {}

// Available 表示非 Windows 平台回退为浏览器访问，不支持自绘标题栏的窗口操作。
func (d *Desktop) Available() bool { return false }

// Minimise 在浏览器模式下不可用。
func (d *Desktop) Minimise() {}

// ToggleMaximise 在浏览器模式下不可用。
func (d *Desktop) ToggleMaximise() bool { return false }

// IsMaximised 在浏览器模式下不可用。
func (d *Desktop) IsMaximised() bool { return false }

// Close 在浏览器模式下不可用。
func (d *Desktop) Close() {}

// SetTrayApps 在非 Windows 平台无需处理。
func (d *Desktop) SetTrayApps([]AppInfo) {}

// Quit 在非 Windows 平台无需处理，退出由信号触发。
func (d *Desktop) Quit() {}
