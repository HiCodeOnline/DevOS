package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SettingsFile 是应用锁设置的持久化文件名。
const SettingsFile = "security.json"

// 默认与内置壁纸预设。
const (
	WallpaperPreset = "preset"
	WallpaperCustom = "custom"
	DefaultPreset   = "aurora"
)

// 支持的语言标识；新增语言时在此登记并在前端补充词典。
var SupportedLocales = []string{"zh-CN", "en", "zh-TW"}

// settingsVersion 为当前设置文件版本；版本 2 起语言为空表示跟随系统。
const settingsVersion = 2

// Wallpaper 描述当前壁纸来源。
type Wallpaper struct {
	Kind   string `json:"kind"`             // preset | custom
	Preset string `json:"preset,omitempty"` // 预设标识
	File   string `json:"file,omitempty"`   // 自定义壁纸文件名（位于 dataDir/wallpapers）
}

// Settings 是 security.json 的结构。
type Settings struct {
	Version   int             `json:"version"`
	Locale    string          `json:"locale"`
	Wallpaper Wallpaper       `json:"wallpaper"`
	Password  *PasswordRecord `json:"password,omitempty"`
}

// Store 管理安全设置文件，保证并发安全与原子落盘。
type Store struct {
	mu   sync.Mutex
	path string
	data Settings
}

// Open 读取（或创建）安全设置文件。
func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("安全设置目录不能为空")
	}
	s := &Store{
		path: filepath.Join(dir, SettingsFile),
		data: Settings{
			Version:   settingsVersion,
			Locale:    "",
			Wallpaper: Wallpaper{Kind: WallpaperPreset, Preset: DefaultPreset},
		},
	}
	raw, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("安全设置文件损坏: %w", err)
		}
		if s.data.Version < settingsVersion {
			// 旧版本的语言总是被写成默认值，视为未选择，迁移为跟随系统。
			s.data.Locale = ""
			s.data.Version = settingsVersion
		} else {
			s.data.Locale = NormalizeLocale(s.data.Locale)
		}
		if s.data.Wallpaper.Kind != WallpaperCustom && s.data.Wallpaper.Kind != WallpaperPreset {
			s.data.Wallpaper = Wallpaper{Kind: WallpaperPreset, Preset: DefaultPreset}
		}
		if s.data.Wallpaper.Kind == WallpaperPreset && s.data.Wallpaper.Preset == "" {
			s.data.Wallpaper.Preset = DefaultPreset
		}
	case os.IsNotExist(err):
		// 首次运行：写入默认设置。
	default:
		return nil, err
	}
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

// NormalizeLocale 把语言标识归一为受支持的值；空值与未知语言返回空，表示跟随系统。
func NormalizeLocale(locale string) string {
	value := strings.TrimSpace(locale)
	switch {
	case value == "":
		return ""
	case strings.EqualFold(value, "zh-CN"), strings.EqualFold(value, "zh-Hans"), strings.EqualFold(value, "zh"),
		strings.HasPrefix(strings.ToLower(value), "zh-cn"), strings.HasPrefix(strings.ToLower(value), "zh-hans"):
		return "zh-CN"
	case strings.EqualFold(value, "zh-TW"), strings.EqualFold(value, "zh-Hant"), strings.HasPrefix(strings.ToLower(value), "zh-tw"),
		strings.HasPrefix(strings.ToLower(value), "zh-hant"), strings.EqualFold(value, "zh-HK"):
		return "zh-TW"
	case strings.EqualFold(value, "en"), strings.HasPrefix(strings.ToLower(value), "en-"):
		return "en"
	default:
		return ""
	}
}

func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".security-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// Snapshot 返回设置副本，避免调用方直接修改内部状态。
func (s *Store) Snapshot() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.data
	if s.data.Password != nil {
		pw := *s.data.Password
		out.Password = &pw
	}
	return out
}

// HasPassword 返回是否已设置应用密码。
func (s *Store) HasPassword() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Password != nil
}

// Locale 返回当前语言。
func (s *Store) Locale() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Locale
}

// Wallpaper 返回当前壁纸。
func (s *Store) Wallpaper() Wallpaper {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Wallpaper
}

// Verify 校验应用密码。
func (s *Store) Verify(password string) error {
	s.mu.Lock()
	record := s.data.Password
	s.mu.Unlock()
	if record == nil {
		return ErrNoPassword
	}
	if !record.verify(password) {
		return ErrWrongPassword
	}
	return nil
}

// SetPassword 首次设置应用密码。
func (s *Store) SetPassword(password string) error {
	record, err := hashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Password != nil {
		return errors.New("已设置应用密码，请使用修改密码")
	}
	s.data.Password = record
	return s.persistLocked()
}

// ChangePassword 验证旧密码后设置新密码。
func (s *Store) ChangePassword(oldPassword, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	s.mu.Lock()
	record := s.data.Password
	s.mu.Unlock()
	if record == nil {
		return ErrNoPassword
	}
	if !record.verify(oldPassword) {
		return ErrWrongPassword
	}
	next, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Password = next
	return s.persistLocked()
}

// RemovePassword 验证旧密码后移除应用密码。
func (s *Store) RemovePassword(oldPassword string) error {
	s.mu.Lock()
	record := s.data.Password
	s.mu.Unlock()
	if record == nil {
		return ErrNoPassword
	}
	if !record.verify(oldPassword) {
		return ErrWrongPassword
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Password = nil
	return s.persistLocked()
}

// SetLocale 更新语言并持久化。
func (s *Store) SetLocale(locale string) error {
	normalized := NormalizeLocale(locale)
	if normalized == "" {
		return errors.New("不支持的语言")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Locale = normalized
	return s.persistLocked()
}

// SetWallpaper 更新壁纸并持久化。
func (s *Store) SetWallpaper(w Wallpaper) error {
	if w.Kind != WallpaperPreset && w.Kind != WallpaperCustom {
		return errors.New("壁纸类型不正确")
	}
	if w.Kind == WallpaperPreset && strings.TrimSpace(w.Preset) == "" {
		w.Preset = DefaultPreset
	}
	if w.Kind == WallpaperCustom && strings.TrimSpace(w.File) == "" {
		return errors.New("自定义壁纸文件名不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Wallpaper = w
	return s.persistLocked()
}
