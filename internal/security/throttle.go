package security

import (
	"sync"
	"time"
)

// Throttle 在连续密码错误后施加指数退避，降低暴力猜测风险。
type Throttle struct {
	mu       sync.Mutex
	failures int
	until    time.Time
}

const (
	failThreshold = 5
	maxBackoff    = 30 * time.Second
)

// Blocked 返回仍需等待的时间；无需等待时返回 0。
func (t *Throttle) Blocked() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.until.IsZero() {
		return 0
	}
	if remaining := time.Until(t.until); remaining > 0 {
		return remaining
	}
	t.until = time.Time{}
	return 0
}

// Failure 记录一次失败；超过阈值后指数增加等待时间。
func (t *Throttle) Failure() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures++
	if t.failures <= failThreshold {
		return
	}
	delay := time.Second
	for i := failThreshold; i < t.failures && delay < maxBackoff; i++ {
		delay *= 2
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	t.until = time.Now().Add(delay)
}

// Success 在密码正确后清空失败计数。
func (t *Throttle) Success() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures = 0
	t.until = time.Time{}
}
