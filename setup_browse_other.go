//go:build !windows

package main

import "errors"

// browseForFolder 在当前系统上不支持原生目录选择。
func browseForFolder(string) (string, error) {
	return "", errors.New("当前系统请手动填写目录路径")
}
