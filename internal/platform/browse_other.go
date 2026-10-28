//go:build !windows

package platform

import "errors"

// BrowseForFolder 在当前系统上不支持原生目录选择。
func BrowseForFolder(string) (string, error) {
	return "", errors.New("当前系统请手动填写目录路径")
}
