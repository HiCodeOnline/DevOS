//go:build !cgo

package dbapp

// duckdbSupport 在纯 Go 构建中不可用：DuckDB 驱动依赖 CGO 原生库。
func duckdbSupport() string { return SupportPlanned }
