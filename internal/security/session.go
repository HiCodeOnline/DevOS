package security

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// SessionCookie 是解锁会话的 Cookie 名称。
const SessionCookie = "devos_session"

// Sessions 在内存中维护解锁令牌，进程重启后全部失效。
type Sessions struct {
	mu     sync.Mutex
	ttl    time.Duration
	tokens map[string]time.Time
}

// NewSessions 创建会话表，ttl <= 0 时默认 24 小时。
func NewSessions(ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Sessions{ttl: ttl, tokens: map[string]time.Time{}}
}

// Issue 生成一个新的随机会话令牌。
func (s *Sessions) Issue() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expiry := range s.tokens {
		if now.After(expiry) {
			delete(s.tokens, key)
		}
	}
	s.tokens[token] = now.Add(s.ttl)
	return token
}

// Valid 校验令牌并顺延有效期。
func (s *Sessions) Valid(token string) bool {
	if token == "" {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.tokens[token]
	if !ok {
		return false
	}
	if now.After(expiry) {
		delete(s.tokens, token)
		return false
	}
	s.tokens[token] = now.Add(s.ttl)
	return true
}

// Revoke 使单个令牌失效。
func (s *Sessions) Revoke(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// RevokeAll 使全部令牌失效（修改密码、移除密码时调用）。
func (s *Sessions) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = map[string]time.Time{}
}
