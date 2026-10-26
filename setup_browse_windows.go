//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// browseForFolder 调用系统文件夹选择对话框，返回用户选中的目录。
func browseForFolder(start string) (string, error) {
	script := `[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; ` +
		`Add-Type -AssemblyName System.Windows.Forms; ` +
		`$d=New-Object System.Windows.Forms.FolderBrowserDialog; ` +
		`$d.Description='选择 DevOS 数据存储目录'; $d.ShowNewFolderButton=$true; ` +
		`if ($env:DEVOS_SETUP_START) { $d.SelectedPath=$env:DEVOS_SETUP_START }; ` +
		`if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.SelectedPath) }`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "DEVOS_SETUP_START="+start)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("无法打开目录选择窗口，请手动填写路径")
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("未选择目录")
	}
	return dir, nil
}
