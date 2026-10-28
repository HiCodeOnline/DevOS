//go:build windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// BrowseForFolder 调用系统文件夹选择对话框，返回用户选中的目录。
func BrowseForFolder(start string) (string, error) {
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

// PickFile 调用系统文件选择对话框，返回用户选中的文件路径。
// filter 使用 Windows 对话框格式，例如 "图片 (*.png;*.jpg)|*.png;*.jpg|所有文件 (*.*)|*.*"。
func PickFile(start, filter string) (string, error) {
	script := `[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; ` +
		`Add-Type -AssemblyName System.Windows.Forms; ` +
		`$d=New-Object System.Windows.Forms.OpenFileDialog; ` +
		`$d.Title='选择文件'; $d.CheckFileExists=$true; $d.Multiselect=$false; ` +
		`if ($env:DEVOS_PICK_FILTER) { $d.Filter=$env:DEVOS_PICK_FILTER } else { $d.Filter='所有文件 (*.*)|*.*' }; ` +
		`if ($env:DEVOS_PICK_START) { $d.InitialDirectory=$env:DEVOS_PICK_START }; ` +
		`if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.FileName) }`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "DEVOS_PICK_START="+start, "DEVOS_PICK_FILTER="+filter)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("无法打开文件选择窗口，请手动填写文件路径")
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", errors.New("未选择文件")
	}
	return path, nil
}

// RevealInFolder 在系统文件管理器中定位到指定文件。
func RevealInFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("路径不能为空")
	}
	if _, err := os.Stat(path); err != nil {
		dir := filepath.Dir(path)
		if dir == "" || dir == "." {
			return err
		}
		return exec.Command("explorer.exe", dir).Start()
	}
	return exec.Command("explorer.exe", "/select,"+path).Start()
}
