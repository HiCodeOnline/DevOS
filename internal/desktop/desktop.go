// Package desktop 提供 DevOS 的原生桌面窗口与托盘能力。
package desktop

import "log/slog"

// AppInfo 是托盘菜单中可快速打开的插件应用。
type AppInfo struct {
	ID   string
	Name string
}

// Options 是创建桌面应用的配置。
type Options struct {
	// Title 主窗口标题。
	Title string
	// Icon 窗口与托盘图标（PNG 编码）。
	Icon []byte
	// Logger 日志输出。
	Logger *slog.Logger
	// InitialURL 主窗口首次加载的地址。
	InitialURL string
	// Show 为 true 时启动即显示主窗口，否则仅驻留托盘。
	Show bool
	// Tray 为 true 时创建托盘图标，关闭窗口只隐藏而不退出。
	Tray bool
	// DataDir 返回当前数据目录，供托盘「打开数据目录」使用。
	DataDir func() string
	// OnQuit 在应用退出前调用，用于停止后台服务。
	OnQuit func()
}
