// Package sqlstore 为各应用提供按领域拆分的本地 SQLite 存储。
//
// 每个应用使用独立的数据库文件，避免多个写入方争用同一把 SQLite 写锁；
// 打开时按 PRAGMA user_version 顺序执行迁移，便于后续版本升级表结构。
package sqlstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动
)

// Open 打开 dir 下的 name 数据库，并把表结构迁移到最新版本。
//
// 单个数据库限制一个连接，避免并发写入时的 SQLITE_BUSY；WAL 模式让读写互不阻塞。
func Open(dir, name string, migrations []string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	path := filepath.Join(dir, name)
	dsn := filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s 失败: %w", name, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrate(db, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	// 数据库内含凭据密文，尽量收紧文件权限（Windows 上为尽力而为）。
	if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// migrate 按 user_version 依次执行未应用的迁移。
func migrate(db *sql.DB, migrations []string) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取数据库版本失败: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("数据库版本 %d 高于当前程序支持的 %d，请升级 DevOS", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("数据库迁移到版本 %d 失败: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("记录数据库版本失败: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// BackupLegacy 把已迁移的历史 JSON 文件改名为 .bak 保留备份。
// 文件不存在时直接返回 nil，便于重复调用。
func BackupLegacy(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	target := path + ".bak"
	_ = os.Remove(target)
	return os.Rename(path, target)
}
