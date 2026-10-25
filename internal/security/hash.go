// Package security 提供 DevOS 的应用锁能力：密码哈希、设置持久化与解锁会话。
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN  = 1 << 15
	scryptR  = 8
	scryptP  = 1
	keyLen   = 32
	saltLen  = 16
	minRunes = 6
	maxRunes = 128
	maxN     = 1 << 20
	maxR     = 32
	maxP     = 8
	hashAlg  = "scrypt"
)

var (
	// ErrWeakPassword 表示新密码不满足长度要求。
	ErrWeakPassword = fmt.Errorf("应用密码至少需要 %d 个字符", minRunes)
	// ErrWrongPassword 表示密码校验失败。
	ErrWrongPassword = errors.New("密码不正确")
	// ErrNoPassword 表示尚未设置应用密码。
	ErrNoPassword = errors.New("尚未设置应用密码")
)

// PasswordRecord 是可持久化的密码哈希记录。
type PasswordRecord struct {
	Algorithm string `json:"algorithm"`
	N         int    `json:"n"`
	R         int    `json:"r"`
	P         int    `json:"p"`
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
}

// 串行化 scrypt 校验，避免并发请求瞬时占用过多内存。
var verifySem = make(chan struct{}, 1)

// ValidatePassword 校验应用密码的格式要求（任意字符，至少 6 个字符）。
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < minRunes || len([]rune(password)) > maxRunes {
		return ErrWeakPassword
	}
	if strings.TrimSpace(password) == "" {
		return ErrWeakPassword
	}
	return nil
}

func hashPassword(password string) (*PasswordRecord, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, err
	}
	return &PasswordRecord{
		Algorithm: hashAlg,
		N:         scryptN,
		R:         scryptR,
		P:         scryptP,
		Salt:      base64.StdEncoding.EncodeToString(salt),
		Hash:      base64.StdEncoding.EncodeToString(key),
	}, nil
}

// verify 用恒定时间比较校验密码。
func (r *PasswordRecord) verify(password string) bool {
	if r == nil || r.Hash == "" || r.Salt == "" {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(r.Salt)
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(r.Hash)
	if err != nil || len(expected) == 0 {
		return false
	}
	n, rp, p := r.N, r.R, r.P
	// 防止损坏或恶意构造的参数导致资源耗尽。
	if n < 1<<14 || n > maxN || n&(n-1) != 0 {
		n = scryptN
	}
	if rp < 1 || rp > maxR {
		rp = scryptR
	}
	if p < 1 || p > maxP {
		p = scryptP
	}

	verifySem <- struct{}{}
	key, err := scrypt.Key([]byte(password), salt, n, rp, p, len(expected))
	<-verifySem
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, expected) == 1
}
