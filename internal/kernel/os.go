package kernel

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

// OpenBrowser 使用系统默认浏览器打开 URL。
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// OpenPath 使用系统文件管理器打开目录。
func OpenPath(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

// WaitSignal 阻塞直到收到退出信号，并执行回调。
func WaitSignal(onSignal func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	onSignal()
}
