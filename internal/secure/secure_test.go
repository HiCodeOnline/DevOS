package secure

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestKeyringPersistsAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, "test.key")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	encrypted, err := first.Encrypt("p@ssw0rd")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !IsEncrypted(encrypted) || strings.Contains(encrypted, "p@ssw0rd") {
		t.Fatalf("密文不符合预期: %q", encrypted)
	}
	if got, err := first.Decrypt(encrypted); err != nil || got != "p@ssw0rd" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
	if empty, _ := first.Encrypt(""); empty != "" {
		t.Fatalf("空值不应加密: %q", empty)
	}
	if plain, _ := first.Decrypt("plain"); plain != "plain" {
		t.Fatalf("明文应原样返回: %q", plain)
	}

	second, err := Open(dir, "test.key")
	if err != nil {
		t.Fatalf("再次 Open: %v", err)
	}
	if got, err := second.Decrypt(encrypted); err != nil || got != "p@ssw0rd" {
		t.Fatalf("使用持久化密钥解密失败: %q, %v", got, err)
	}
}

func TestExportPasswordFormat(t *testing.T) {
	password, err := NewExportPassword()
	if err != nil {
		t.Fatalf("NewExportPassword: %v", err)
	}
	groups := strings.Split(password, "-")
	if len(groups) != 6 {
		t.Fatalf("分组数量 = %d, 期望 6", len(groups))
	}
	for _, group := range groups {
		if len(group) != 4 {
			t.Fatalf("分组长度 = %d, 期望 4", len(group))
		}
	}
}

func TestSealAndUnseal(t *testing.T) {
	password, err := NewExportPassword()
	if err != nil {
		t.Fatalf("NewExportPassword: %v", err)
	}
	payload := []byte(`[{"name":"prod","password":"p@ss"}]`)
	raw, err := Seal(password, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !IsEnvelope(raw) {
		t.Fatalf("导出内容未被识别为加密文件")
	}
	if strings.Contains(string(raw), "p@ss") {
		t.Fatalf("导出文件中出现明文密码")
	}

	// 大小写与分隔符差异不影响解密。
	loose := " " + strings.ToLower(strings.ReplaceAll(password, "-", " ")) + " "
	plain, err := Unseal(loose, raw)
	if err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	if string(plain) != string(payload) {
		t.Fatalf("解密内容 = %s", plain)
	}

	if _, err := Unseal("AAAA-BBBB-CCCC-DDDD-EEEE-FFFF", raw); err == nil {
		t.Fatalf("错误密钥不应通过")
	}
	if _, err := Unseal("short", raw); err == nil {
		t.Fatalf("过短密钥不应通过")
	}
}

func TestUnsealRejectsTamperedData(t *testing.T) {
	password, _ := NewExportPassword()
	raw, err := Seal(password, []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	data[0] ^= 0xff
	env.Data = base64.StdEncoding.EncodeToString(data)
	tampered, _ := json.Marshal(env)
	if _, err := Unseal(password, tampered); err == nil {
		t.Fatalf("篡改后的内容不应通过校验")
	}
}

func TestIsEnvelopeOnLegacyJSON(t *testing.T) {
	if IsEnvelope([]byte(`[{"host":"example.com"}]`)) {
		t.Fatalf("历史明文数组不应识别为加密文件")
	}
	if IsEnvelope([]byte(`{"format":"other"}`)) {
		t.Fatalf("其他格式不应识别为加密文件")
	}
}
