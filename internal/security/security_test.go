package security

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store, dir
}

func TestHashRoundTripAndWeakPassword(t *testing.T) {
	if err := ValidatePassword("12345"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("短密码应被拒绝: %v", err)
	}
	if err := ValidatePassword("      "); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("全空格密码应被拒绝: %v", err)
	}
	if err := ValidatePassword("abc123"); err != nil {
		t.Fatalf("合法密码不应报错: %v", err)
	}

	first, err := hashPassword("abc123")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	second, err := hashPassword("abc123")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if first.Salt == second.Salt {
		t.Fatal("两次哈希的 salt 不应相同")
	}
	if !first.verify("abc123") {
		t.Fatal("正确密码校验失败")
	}
	if first.verify("abc124") {
		t.Fatal("错误密码不应通过")
	}
}

func TestStorePasswordLifecycle(t *testing.T) {
	store, dir := newStore(t)
	if store.HasPassword() {
		t.Fatal("初始状态不应有密码")
	}
	if err := store.Verify("anything"); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("未设置密码时应返回 ErrNoPassword: %v", err)
	}
	if err := store.SetPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("短密码应被拒绝: %v", err)
	}
	if err := store.SetPassword("first-pass"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if !store.HasPassword() {
		t.Fatal("设置后应有密码")
	}
	if err := store.Verify("first-pass"); err != nil {
		t.Fatalf("正确密码校验失败: %v", err)
	}
	if err := store.Verify("wrong-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("错误密码应返回 ErrWrongPassword: %v", err)
	}
	if err := store.ChangePassword("wrong-pass", "second-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("修改密码时旧密码错误应失败: %v", err)
	}
	if err := store.ChangePassword("first-pass", "second-pass"); err != nil {
		t.Fatalf("修改密码失败: %v", err)
	}
	if err := store.Verify("first-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("旧密码应失效: %v", err)
	}
	if err := store.Verify("second-pass"); err != nil {
		t.Fatalf("新密码应有效: %v", err)
	}
	if err := store.RemovePassword("bad-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("移除密码时旧密码错误应失败: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, SettingsFile))
	if err != nil {
		t.Fatalf("读取设置文件: %v", err)
	}
	if strings.Contains(string(raw), "second-pass") {
		t.Fatal("设置文件中不应出现明文密码")
	}
	if !strings.Contains(string(raw), `"algorithm": "scrypt"`) {
		t.Fatalf("设置文件缺少哈希记录: %s", raw)
	}

	if err := store.RemovePassword("second-pass"); err != nil {
		t.Fatalf("移除密码失败: %v", err)
	}
	if store.HasPassword() {
		t.Fatal("移除后不应有密码")
	}

	// 模拟重启后重新加载。
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	if reopened.HasPassword() {
		t.Fatal("重启后不应有密码")
	}
}

func TestStorePersistsLocaleAndWallpaper(t *testing.T) {
	store, dir := newStore(t)
	if store.Locale() != "" {
		t.Fatalf("默认语言应为空（跟随系统），实际 %q", store.Locale())
	}
	if err := store.SetLocale("en-US"); err != nil {
		t.Fatalf("SetLocale: %v", err)
	}
	if store.Locale() != "en" {
		t.Fatalf("语言应归一为 en，实际 %q", store.Locale())
	}
	if err := store.SetLocale("pt"); err == nil {
		t.Fatal("不支持的语言应报错")
	}
	if err := store.SetWallpaper(Wallpaper{Kind: WallpaperCustom, File: "wallpaper-1-a1b2c3d4.png"}); err != nil {
		t.Fatalf("SetWallpaper: %v", err)
	}
	if err := store.SetWallpaper(Wallpaper{Kind: "unknown"}); err == nil {
		t.Fatal("非法壁纸类型应报错")
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	if reopened.Locale() != "en" {
		t.Fatalf("重启后语言 = %q", reopened.Locale())
	}
	wp := reopened.Wallpaper()
	if wp.Kind != WallpaperCustom || wp.File != "wallpaper-1-a1b2c3d4.png" {
		t.Fatalf("重启后壁纸 = %#v", wp)
	}
}

func TestStoreCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("损坏的设置文件应返回错误")
	}
}

func TestStoreConcurrentWrites(t *testing.T) {
	store, _ := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = store.SetLocale("en")
			_ = store.SetWallpaper(Wallpaper{Kind: WallpaperPreset, Preset: DefaultPreset})
		}(i)
	}
	wg.Wait()
	if store.Locale() != "en" {
		t.Fatalf("并发写入后语言 = %q", store.Locale())
	}
}

func TestSessionsLifecycle(t *testing.T) {
	s := NewSessions(50 * time.Millisecond)
	token := s.Issue()
	if token == "" || !s.Valid(token) {
		t.Fatal("新令牌应有效")
	}
	s.Revoke(token)
	if s.Valid(token) {
		t.Fatal("撤销后令牌应失效")
	}
	token = s.Issue()
	time.Sleep(60 * time.Millisecond)
	if s.Valid(token) {
		t.Fatal("过期令牌应失效")
	}

	s = NewSessions(time.Hour)
	a, b := s.Issue(), s.Issue()
	s.RevokeAll()
	if s.Valid(a) || s.Valid(b) {
		t.Fatal("RevokeAll 后全部令牌应失效")
	}
}

func TestThrottleBackoff(t *testing.T) {
	th := &Throttle{}
	for i := 0; i < failThreshold; i++ {
		th.Failure()
		if d := th.Blocked(); d != 0 {
			t.Fatalf("达到阈值前不应退避，实际 %v", d)
		}
	}
	th.Failure()
	if d := th.Blocked(); d <= 0 {
		t.Fatal("超过阈值后应退避")
	}
	th.Success()
	if d := th.Blocked(); d != 0 {
		t.Fatalf("成功后不应退避，实际 %v", d)
	}
}
