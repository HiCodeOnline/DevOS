package profile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Profile 描述一个 SSH/SFTP 连接配置，字段设计参照 Xshell 的会话属性。
type Profile struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Group         string `json:"group,omitempty"`
	Description   string `json:"description,omitempty"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Auth          string `json:"auth"` // password | key
	Password      string `json:"password,omitempty"`
	PrivateKey    string `json:"privateKey,omitempty"`
	Passphrase    string `json:"passphrase,omitempty"`
	JumpProfileID string `json:"jumpProfileId,omitempty"`
	TerminalType  string `json:"terminalType,omitempty"`
	Encoding      string `json:"encoding,omitempty"`
	StartupCmd    string `json:"startupCommand,omitempty"`
	KeepAliveSec  int    `json:"keepAliveSec,omitempty"`
	LogEnabled    bool   `json:"logEnabled,omitempty"`
	FontSize      int    `json:"fontSize,omitempty"`
	ColorScheme   string `json:"colorScheme,omitempty"`
	AutoReconnect bool   `json:"autoReconnect,omitempty"`
}

// ApplyDefaults 为新建会话补齐默认值。
func (p *Profile) ApplyDefaults() {
	if p.Port == 0 {
		p.Port = 22
	}
	if p.Auth == "" {
		p.Auth = "password"
	}
	if p.TerminalType == "" {
		p.TerminalType = "xterm-256color"
	}
	if p.Encoding == "" {
		p.Encoding = "utf-8"
	}
	if p.KeepAliveSec < 0 {
		p.KeepAliveSec = 0
	}
	if p.KeepAliveSec == 0 {
		p.KeepAliveSec = 30
	}
	if p.FontSize <= 0 {
		p.FontSize = 13
	}
	if p.ColorScheme == "" {
		p.ColorScheme = "devos"
	}
}

// 会话运行期使用的取值回落（不修改已保存的配置）。
func (p Profile) TerminalOrDefault() string {
	if p.TerminalType == "" {
		return "xterm-256color"
	}
	return p.TerminalType
}

func (p Profile) EncodingOrDefault() string {
	if p.Encoding == "" {
		return "utf-8"
	}
	return p.Encoding
}

func (p Profile) FontSizeOrDefault() int {
	if p.FontSize <= 0 {
		return 13
	}
	return p.FontSize
}

func (p Profile) ColorSchemeOrDefault() string {
	if p.ColorScheme == "" {
		return "devos"
	}
	return p.ColorScheme
}

// Store 以 JSON 文件持久化连接配置，供 SSH 与 SFTP 插件共享。
type Store struct {
	mu   sync.RWMutex
	path string
	list []Profile
}

var (
	storesMu sync.Mutex
	stores   = map[string]*Store{}
)

// Open 按目录返回共享的配置存储，同一路径只会加载一次。
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, "profiles.json")
	storesMu.Lock()
	defer storesMu.Unlock()
	if s, ok := stores[path]; ok {
		return s, nil
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s.list); err != nil {
			return nil, fmt.Errorf("解析连接配置失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	stores[path] = s
	return s, nil
}

func (s *Store) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Profile{}, s.list...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Store) Get(id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.list {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Save 新增或更新配置，返回保存后的配置。
func (s *Store) Save(p Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	isNew := p.ID == ""
	if isNew {
		p.ID = newID()
		p.ApplyDefaults()
	} else if p.Port == 0 {
		p.Port = 22
	}

	found := false
	for i := range s.list {
		if s.list[i].ID == p.ID {
			s.list[i] = p
			found = true
			break
		}
	}
	if !found {
		s.list = append(s.list, p)
	}
	return p, s.persist()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			return s.persist()
		}
	}
	return nil
}

// Duplicate 复制一个会话（名称追加「副本」）。
func (s *Store) Duplicate(id string) (Profile, error) {
	src, ok := s.Get(id)
	if !ok {
		return Profile{}, fmt.Errorf("会话不存在")
	}
	src.ID = ""
	src.Name = src.Name + " 副本"
	return s.Save(src)
}

// Import 导入会话列表，ID 冲突时自动分配新 ID，返回导入数量。
func (s *Store) Import(items []Profile) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make(map[string]bool, len(s.list))
	for _, p := range s.list {
		ids[p.ID] = true
	}

	count := 0
	for _, p := range items {
		if p.Host == "" {
			continue
		}
		if p.ID == "" || ids[p.ID] {
			p.ID = newID()
			p.Name = p.Name + " (导入)"
		}
		p.ApplyDefaults()
		ids[p.ID] = true
		s.list = append(s.list, p)
		count++
	}
	if count == 0 {
		return 0, nil
	}
	return count, s.persist()
}

func (s *Store) persist() error {
	data, err := json.MarshalIndent(s.list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}

func newID() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
