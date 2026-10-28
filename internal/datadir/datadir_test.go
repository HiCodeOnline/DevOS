package datadir

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolateConfig 把用户配置目录指向临时目录，避免测试读写真实配置。
func isolateConfig(t *testing.T) {
	t.Helper()
	base := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", base)
	} else {
		t.Setenv("XDG_CONFIG_HOME", base)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBackupAndExtract(t *testing.T) {
	src := filepath.Join(t.TempDir(), "data")
	writeFile(t, filepath.Join(src, "config.json"), `{"port":7788}`)
	writeFile(t, filepath.Join(src, "apps", "db", "connections.db"), "sqlite")

	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := BackupToFile(archive, src); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := VerifyArchive(archive); err != nil {
		t.Fatalf("verify: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "restored")
	if err := ExtractArchive(archive, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "config.json"))
	if err != nil || string(data) != `{"port":7788}` {
		t.Fatalf("restored config = %q, err = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, BackupMarker)); !os.IsNotExist(err) {
		t.Fatalf("marker should not be extracted into data dir")
	}
}

func TestBackupRejectsForeignArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "foreign.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	if _, err := zw.Create("readme.txt"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := VerifyArchive(archive); err == nil {
		t.Fatal("expected foreign archive to be rejected")
	}
}

func TestApplyPendingRestore(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	writeFile(t, filepath.Join(data, "config.json"), "old")

	source := filepath.Join(root, "source")
	writeFile(t, filepath.Join(source, "config.json"), "new")
	archive := filepath.Join(root, "backup.zip")
	if err := BackupToFile(archive, source); err != nil {
		t.Fatal(err)
	}
	if err := QueueRestore(archive); err != nil {
		t.Fatalf("queue restore: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := ApplyPending(log, data)
	if got != data {
		t.Fatalf("data dir changed to %q", got)
	}
	content, err := os.ReadFile(filepath.Join(data, "config.json"))
	if err != nil || string(content) != "new" {
		t.Fatalf("restored content = %q, err = %v", content, err)
	}
	if restore, _ := PendingState(); restore {
		t.Fatal("pending restore should be cleared")
	}
}

// 默认安装时数据目录与配置目录重合，还原必须保留位置记录与备份缓存。
func TestApplyPendingRestoreIntoConfigRoot(t *testing.T) {
	isolateConfig(t)
	root, err := ConfigRoot()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "config.json"), "old")
	writeFile(t, filepath.Join(root, "vault.db"), "old-vault")
	if err := WriteLocation(root); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "source")
	writeFile(t, filepath.Join(source, "config.json"), "new")
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err := BackupToFile(archive, source); err != nil {
		t.Fatal(err)
	}
	if err := QueueRestore(archive); err != nil {
		t.Fatalf("queue restore: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if got := ApplyPending(log, root); got != root {
		t.Fatalf("data dir changed to %q", got)
	}
	content, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil || string(content) != "new" {
		t.Fatalf("restored config = %q, err = %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(root, "vault.db")); !os.IsNotExist(err) {
		t.Fatalf("旧数据应被清除: %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "location.json"))
	if err != nil {
		t.Fatalf("位置记录应保留: %v", err)
	}
	var loc location
	if err := json.Unmarshal(payload, &loc); err != nil || !samePath(loc.DataDir, root) {
		t.Fatalf("位置记录内容不匹配: %q, err = %v", payload, err)
	}
	backups, err := BackupsDir()
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(backups); err != nil || len(entries) == 0 {
		t.Fatalf("还原前自动备份应保留: %v", err)
	}
	if restore, _ := PendingState(); restore {
		t.Fatal("pending restore should be cleared")
	}
}

func TestApplyPendingMigrate(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	writeFile(t, filepath.Join(data, "config.json"), "hello")
	target := filepath.Join(root, "newdata")

	clean, err := QueueMigrate(data, target, false)
	if err != nil {
		t.Fatalf("queue migrate: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := ApplyPending(log, data)
	if got != clean {
		t.Fatalf("ApplyPending returned %q, want %q", got, clean)
	}
	content, err := os.ReadFile(filepath.Join(target, "config.json"))
	if err != nil || string(content) != "hello" {
		t.Fatalf("migrated content = %q, err = %v", content, err)
	}
	loc, err := LocationPath()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(loc)
	if err != nil || !bytes.Contains(payload, []byte("newdata")) {
		t.Fatalf("location.json = %q, err = %v", payload, err)
	}
}

func TestQueueMigrateValidation(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	writeFile(t, filepath.Join(data, "config.json"), "x")
	if _, err := QueueMigrate(data, data, false); err == nil {
		t.Fatal("expected same dir to be rejected")
	}
	if _, err := QueueMigrate(data, filepath.Join(data, "nested"), false); err == nil {
		t.Fatal("expected nested dir to be rejected")
	}
	busy := filepath.Join(root, "busy")
	writeFile(t, filepath.Join(busy, "file.txt"), "x")
	if _, err := QueueMigrate(data, busy, false); err == nil {
		t.Fatal("expected non-empty target to be rejected")
	}
	if _, err := QueueMigrate(data, busy, true); err != nil {
		t.Fatalf("overwrite should be allowed: %v", err)
	}
}
