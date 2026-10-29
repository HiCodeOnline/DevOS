//go:build cgo

package dbapp

import _ "github.com/marcboeker/go-duckdb/v2"

// duckdbSupport 在启用 CGO 的构建中内置 DuckDB 原生驱动。
func duckdbSupport() string { return SupportNative }
