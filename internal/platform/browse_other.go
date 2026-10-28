//go:build !windows

package platform

import "errors"

// BrowseForFolder 在当前系统上不支持原生目录选择。
func BrowseForFolder(string) (string, error) {
	return "", errors.New("当前系统请手动填写目录路径")
}

// PickFile 在当前系统上不支持原生文件选择。
func PickFile(string, string) (string, error) {
	return "", errors.New("当前系统请手动填写文件路径")
}

// RevealInFolder 在当前系统上不支持打开文件管理器。
func RevealInFolder(string) error {
	return errors.New("当前系统不支持打开文件管理器")
}
