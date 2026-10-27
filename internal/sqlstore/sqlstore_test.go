package sqlstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	dir := t.TempDir()
	migrations := []string{
		`CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');`,
		`ALTER TABLE items ADD COLUMN note TEXT NOT NULL DEFAULT '';`,
	}
	db, err := Open(dir, "test.db", migrations)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Exec("INSERT INTO items (id, name) VALUES ('a', 'x')"); err != nil {
		t.Fatalf("写入: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir, "test.db", migrations)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer reopened.Close()
	var version int
	if err := reopened.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("版本 = %d，期望 %d", version, len(migrations))
	}
	var name, note string
	if err := reopened.QueryRow("SELECT name, note FROM items WHERE id = 'a'").Scan(&name, &note); err != nil {
		t.Fatalf("读取数据: %v", err)
	}
	if name != "x" || note != "" {
		t.Fatalf("数据异常: name=%q note=%q", name, note)
	}
}

func TestOpenRejectsNewerVersion(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, "test.db", []string{`CREATE TABLE t (id TEXT);`})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()

	if _, err := Open(dir, "test.db", nil); err == nil {
		t.Fatal("数据库版本高于程序时应报错")
	}
}

func TestBackupLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := BackupLegacy(path); err != nil {
		t.Fatalf("BackupLegacy: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("原文件应被改名")
	}
	if err := BackupLegacy(path); err != nil {
		t.Fatalf("文件不存在时应忽略: %v", err)
	}
}
