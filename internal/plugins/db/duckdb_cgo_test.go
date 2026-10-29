//go:build cgo

package dbapp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestDuckDBMeta 用真实的 DuckDB 文件验证驱动接入与元数据 SQL。
// 仅在启用 CGO 的构建中运行（go-duckdb 依赖 CGO 原生库）。
func TestDuckDBMeta(t *testing.T) {
	file := filepath.Join(t.TempDir(), "probe.duckdb")
	conn := Connection{ID: "c1", Name: "duck", Driver: "duckdb", File: file}
	if !supportsConnect("duckdb") {
		t.Fatal("CGO 构建中 DuckDB 应为可用类型")
	}
	dsn, err := buildDSN(conn, "")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	db, err := sql.Open(driverImpl("duckdb"), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE users(id INTEGER PRIMARY KEY, name VARCHAR NOT NULL, created TIMESTAMP DEFAULT now())`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE VIEW active_users AS SELECT id, name FROM users`); err != nil {
		t.Fatalf("view: %v", err)
	}

	var version string
	if err := db.QueryRowContext(ctx, versionSQL("duckdb")).Scan(&version); err != nil {
		t.Fatalf("version: %v", err)
	}
	if version == "" {
		t.Fatal("版本号为空")
	}

	rows, err := db.QueryContext(ctx, "SELECT table_name, table_type FROM information_schema.tables"+
		" WHERE table_schema = 'main' AND table_type IN ('BASE TABLE','VIEW') ORDER BY table_name")
	if err != nil {
		t.Fatalf("objects: %v", err)
	}
	var names []string
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	rows.Close()
	if len(names) != 2 {
		t.Fatalf("对象列表异常: %v", names)
	}

	pk, err := duckdbPrimaryKeys(ctx, db, "users")
	if err != nil {
		t.Fatalf("pk: %v", err)
	}
	if len(pk) != 1 || !sameNameFold(pk, "id") {
		t.Fatalf("主键解析失败: %v", pk)
	}

	cols, err := db.QueryContext(ctx,
		`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		 FROM information_schema.columns
		 WHERE table_schema = 'main' AND table_name = ?
		 ORDER BY ordinal_position`, "users")
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	defer cols.Close()
	var got []string
	for cols.Next() {
		var name, typ, nullable, dflt string
		if err := cols.Scan(&name, &typ, &nullable, &dflt); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if len(got) != 3 {
		t.Fatalf("列数量异常: %v", got)
	}

	// 带参数的 DSN 应能被驱动接受。
	if _, err := buildDSN(Connection{ID: "c2", Driver: "duckdb", File: file, Params: "threads=2"}, ""); err != nil {
		t.Fatalf("带参数 DSN: %v", err)
	}
}
