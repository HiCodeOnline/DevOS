// Package secure 为各应用提供敏感数据的加密存储与加密导出能力。
//
// 存储加密：每个应用首次初始化（即安装）时生成 32 字节随机主密钥，
// 保存在应用数据目录下（权限 0600），敏感字段以 AES-256-GCM 加密后落盘。
//
// 导出加密：每次导出随机生成高强度导出密码，用 scrypt 派生密钥后
// 重新加密导出内容；导入时必须提供该密码才能解密。
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	// Prefix 标识已加密的存储字段。
	Prefix = "enc:v1:"

	envelopeFormat = "devos-secure-export"
	envelopeVer    = 1
	masterKeySize  = 32
	scryptN        = 1 << 15
	scryptR        = 8
	scryptP        = 1
)

var errCorrupted = errors.New("加密数据已损坏或密钥不匹配")

// Keyring 持有单个应用的存储主密钥。
type Keyring struct {
	key  []byte
	path string
}

// Open 加载目录下的主密钥；不存在时生成一个新密钥（应用安装时生成）。
func Open(dir, name string) (*Keyring, error) {
	if dir == "" {
		return nil, errors.New("密钥目录不能为空")
	}
	path := filepath.Join(dir, name)
	if raw, err := os.ReadFile(path); err == nil {
		key, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decErr != nil || len(key) != masterKeySize {
			return nil, fmt.Errorf("主密钥文件损坏: %s", path)
		}
		return &Keyring{key: key, path: path}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, err
	}
	return &Keyring{key: key, path: path}, nil
}

// Path 返回主密钥文件路径，便于排查与备份提示。
func (k *Keyring) Path() string { return k.path }

// IsEncrypted 判断某个字段值是否已加密。
func IsEncrypted(value string) bool { return strings.HasPrefix(value, Prefix) }

// Encrypt 加密单个字段；空值保持为空，已加密的值原样返回。
func (k *Keyring) Encrypt(plain string) (string, error) {
	if plain == "" || IsEncrypted(plain) {
		return plain, nil
	}
	if len(k.key) != masterKeySize {
		return "", errors.New("主密钥不可用")
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return Prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密单个字段；明文值原样返回，便于兼容历史数据。
func (k *Keyring) Decrypt(value string) (string, error) {
	if value == "" || !IsEncrypted(value) {
		return value, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, Prefix))
	if err != nil {
		return "", errCorrupted
	}
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errCorrupted
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errCorrupted
	}
	return string(plain), nil
}

// EncryptBytes 使用存储主密钥加密整段内容。
func (k *Keyring) EncryptBytes(data []byte) ([]byte, error) {
	sealed, err := k.Encrypt(string(data))
	if err != nil {
		return nil, err
	}
	return []byte(sealed), nil
}

// DecryptBytes 解密整段内容；若为明文则原样返回。
func (k *Keyring) DecryptBytes(data []byte) ([]byte, error) {
	plain, err := k.Decrypt(string(data))
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}

// NewExportPassword 生成 120 位熵的导出密码，格式为 6 组 4 位大写字母数字。
func NewExportPassword() (string, error) {
	buf := make([]byte, 15) // 15 字节 = 120 位
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	groups := make([]string, 0, len(raw)/4)
	for i := 0; i+4 <= len(raw); i += 4 {
		groups = append(groups, raw[i:i+4])
	}
	return strings.Join(groups, "-"), nil
}

// Envelope 是加密导出文件的格式。
type Envelope struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	Algorithm string `json:"algorithm"`
	KDF       string `json:"kdf"`
	Salt      string `json:"salt"`
	Nonce     string `json:"nonce"`
	CreatedAt string `json:"createdAt"`
	Data      string `json:"data"`
}

// IsEnvelope 判断内容是否为加密导出文件。
func IsEnvelope(raw []byte) bool {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return false
	}
	return env.Format == envelopeFormat && env.Version == envelopeVer
}

// Seal 使用导出密码重新加密导出内容。
func Seal(password string, payload []byte) ([]byte, error) {
	secret, err := normalizePassword(password)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(secret), salt, scryptN, scryptR, scryptP, masterKeySize)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, payload, nil)
	env := Envelope{
		Format:    envelopeFormat,
		Version:   envelopeVer,
		Algorithm: "aes-256-gcm",
		KDF:       fmt.Sprintf("scrypt(N=%d,r=%d,p=%d)", scryptN, scryptR, scryptP),
		Salt:      base64.StdEncoding.EncodeToString(salt),
		Nonce:     base64.StdEncoding.EncodeToString(nonce),
		CreatedAt: time.Now().Format(time.RFC3339),
		Data:      base64.StdEncoding.EncodeToString(sealed),
	}
	return json.MarshalIndent(env, "", "  ")
}

// Unseal 使用导出密码解密导出内容。
func Unseal(password string, raw []byte) ([]byte, error) {
	secret, err := normalizePassword(password)
	if err != nil {
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, errors.New("导出文件格式不正确")
	}
	if env.Format != envelopeFormat || env.Version != envelopeVer {
		return nil, errors.New("不支持的导出文件版本")
	}
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil {
		return nil, errCorrupted
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, errCorrupted
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, errCorrupted
	}
	key, err := scrypt.Key([]byte(secret), salt, scryptN, scryptR, scryptP, masterKeySize)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("导出密钥不正确，或文件内容已被修改")
	}
	return plain, nil
}

// normalizePassword 统一导出密码的大小写与分隔符，校验长度。
func normalizePassword(password string) (string, error) {
	clean := strings.ToUpper(password)
	clean = strings.NewReplacer("-", "", " ", "", "\n", "", "\t", "").Replace(clean)
	if len(clean) < 16 {
		return "", errors.New("导出密钥长度不足")
	}
	return clean, nil
}
