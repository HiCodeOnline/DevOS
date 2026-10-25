//go:build !windows

package kernel

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// TrayApp 是托盘菜单里可快速打开的应用。
type TrayApp struct {
	ID   string
	Name string
}

// RunTray 在非 Windows 平台仅打开浏览器并等待退出信号。
func RunTray(baseURL, dataDir string, openBrowser bool, apps []TrayApp, onQuit func()) {
	if openBrowser && WaitReady(baseURL, 3*time.Second) {
		_ = OpenBrowser(baseURL)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	onQuit()
}
