package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreEncryptsCredentialsAtRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	seed := `[{"id":"a1","name":"生产机","host":"10.0.0.1","user":"root","password":"p@ssw0rd","privateKey":"PRIVATE-KEY","passphrase":"keypass"}]`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("写入初始数据: %v", err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取存储: %v", err)
	}
	for _, secret := range []string{"p@ssw0rd", "PRIVATE-KEY", "keypass"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("存储文件中仍存在明文: %s", secret)
		}
	}
	if !strings.Contains(string(raw), "enc:v1:") {
		t.Fatalf("存储文件未加密: %s", raw)
	}

	items := store.List()
	if len(items) != 1 || items[0].Password != "p@ssw0rd" || items[0].PrivateKey != "PRIVATE-KEY" || items[0].Passphrase != "keypass" {
		t.Fatalf("内存中的凭据未正确解密: %#v", items)
	}

	// 模拟重启：清除进程内缓存后重新加载。
	storesMu.Lock()
	delete(stores, path)
	storesMu.Unlock()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	again := reopened.List()
	if len(again) != 1 || again[0].Password != "p@ssw0rd" {
		t.Fatalf("重启后解密失败: %#v", again)
	}
}

func TestStoreSaveEncryptsNewCredentials(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Save(Profile{Name: "测试", Host: "example.com", User: "admin", Password: "secret-123"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatalf("读取存储: %v", err)
	}
	if strings.Contains(string(raw), "secret-123") {
		t.Fatalf("保存后仍存在明文密码")
	}
	if got := store.List()[0].Password; got != "secret-123" {
		t.Fatalf("内存密码 = %q", got)
	}
}
