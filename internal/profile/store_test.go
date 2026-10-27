package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreMigratesLegacyJSONAndEncryptsCredentials(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "profiles.json")
	seed := `[{"id":"a1","name":"生产机","host":"10.0.0.1","user":"root","password":"p@ssw0rd","privateKey":"PRIVATE-KEY","passphrase":"keypass"}]`
	if err := os.WriteFile(legacy, []byte(seed), 0o600); err != nil {
		t.Fatalf("写入初始数据: %v", err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Fatalf("历史 JSON 未保留备份: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("历史 JSON 未迁移: %v", err)
	}

	dbPath := filepath.Join(dir, "profiles.db")
	raw := readDBFiles(t, dbPath)
	for _, secret := range []string{"p@ssw0rd", "PRIVATE-KEY", "keypass"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("数据库中仍存在明文: %s", secret)
		}
	}
	if !strings.Contains(string(raw), "enc:v1:") {
		t.Fatalf("凭据未加密落盘")
	}

	items := store.List()
	if len(items) != 1 || items[0].Password != "p@ssw0rd" || items[0].PrivateKey != "PRIVATE-KEY" || items[0].Passphrase != "keypass" {
		t.Fatalf("内存中的凭据未正确解密: %#v", items)
	}

	// 模拟重启：关闭后重新打开同一目录。
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	defer reopened.Close()
	again := reopened.List()
	if len(again) != 1 || again[0].Password != "p@ssw0rd" {
		t.Fatalf("重启后解密失败: %#v", again)
	}
}

// readDBFiles 读取数据库主文件与 WAL，避免只看到未落盘的旧数据。
func readDBFiles(t *testing.T, path string) []byte {
	t.Helper()
	var out []byte
	for _, name := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("读取数据库 %s: %v", name, err)
		}
		out = append(out, data...)
	}
	return out
}

func TestStoreSaveEncryptsNewCredentials(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	saved, err := store.Save(Profile{Name: "测试", Host: "example.com", User: "admin", Password: "secret-123"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.ID == "" {
		t.Fatalf("保存后未分配 ID")
	}

	raw := readDBFiles(t, filepath.Join(dir, "profiles.db"))
	if strings.Contains(string(raw), "secret-123") {
		t.Fatalf("保存后仍存在明文密码")
	}
	if got := store.List()[0].Password; got != "secret-123" {
		t.Fatalf("内存密码 = %q", got)
	}

	// 更新与删除同样作用于数据库。
	saved.Password = "secret-456"
	if _, err := store.Save(saved); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, ok := store.Get(saved.ID)
	if !ok || got.Password != "secret-456" {
		t.Fatalf("更新后读取失败: %#v", got)
	}
	if err := store.Delete(saved.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok := store.Get(saved.ID); ok {
		t.Fatalf("删除后仍可读取")
	}
}
