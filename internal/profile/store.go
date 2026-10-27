package profile

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"devos/internal/secure"
	"devos/internal/sqlstore"
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

// Store 以 SQLite 数据库持久化连接配置，供 SSH 与 SFTP 插件共享。
// 密码、私钥与口令以 AES-256-GCM 加密后落盘。
type Store struct {
	path string
	db   *sql.DB
	keys *secure.Keyring
}

var (
	storesMu sync.Mutex
	stores   = map[string]*Store{}
)

var migrations = []string{`
CREATE TABLE profiles (
	id              TEXT PRIMARY KEY,
	name            TEXT NOT NULL DEFAULT '',
	grp             TEXT NOT NULL DEFAULT '',
	description     TEXT NOT NULL DEFAULT '',
	host            TEXT NOT NULL DEFAULT '',
	port            INTEGER NOT NULL DEFAULT 22,
	user            TEXT NOT NULL DEFAULT '',
	auth            TEXT NOT NULL DEFAULT 'password',
	password        TEXT NOT NULL DEFAULT '',
	private_key     TEXT NOT NULL DEFAULT '',
	passphrase      TEXT NOT NULL DEFAULT '',
	jump_profile_id TEXT NOT NULL DEFAULT '',
	terminal_type   TEXT NOT NULL DEFAULT '',
	encoding        TEXT NOT NULL DEFAULT '',
	startup_command TEXT NOT NULL DEFAULT '',
	keep_alive_sec  INTEGER NOT NULL DEFAULT 0,
	log_enabled     INTEGER NOT NULL DEFAULT 0,
	font_size       INTEGER NOT NULL DEFAULT 0,
	color_scheme    TEXT NOT NULL DEFAULT '',
	auto_reconnect  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_profiles_group ON profiles(grp, name);
`}

const (
	profileColumns = `id, name, grp, description, host, port, user, auth, password,
	private_key, passphrase, jump_profile_id, terminal_type, encoding, startup_command,
	keep_alive_sec, log_enabled, font_size, color_scheme, auto_reconnect`

	upsertProfile = `INSERT INTO profiles (` + profileColumns + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name, grp = excluded.grp, description = excluded.description,
	host = excluded.host, port = excluded.port, user = excluded.user, auth = excluded.auth,
	password = excluded.password, private_key = excluded.private_key,
	passphrase = excluded.passphrase, jump_profile_id = excluded.jump_profile_id,
	terminal_type = excluded.terminal_type, encoding = excluded.encoding,
	startup_command = excluded.startup_command, keep_alive_sec = excluded.keep_alive_sec,
	log_enabled = excluded.log_enabled, font_size = excluded.font_size,
	color_scheme = excluded.color_scheme, auto_reconnect = excluded.auto_reconnect`
)

// Open 按目录返回共享的配置存储，同一路径只会加载一次。
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, "profiles.db")
	storesMu.Lock()
	defer storesMu.Unlock()
	if s, ok := stores[path]; ok {
		return s, nil
	}
	keys, err := secure.Open(dir, "profiles.key")
	if err != nil {
		return nil, fmt.Errorf("初始化会话加密密钥失败: %w", err)
	}
	db, err := sqlstore.Open(dir, "profiles.db", migrations)
	if err != nil {
		return nil, fmt.Errorf("打开会话数据库失败: %w", err)
	}
	s := &Store{path: path, db: db, keys: keys}
	if err := s.migrateLegacy(filepath.Join(dir, "profiles.json")); err != nil {
		_ = db.Close()
		return nil, err
	}
	stores[path] = s
	return s, nil
}

// Close 关闭数据库并从共享缓存中移除，主要供测试与插件卸载使用。
func (s *Store) Close() error {
	storesMu.Lock()
	delete(stores, s.path)
	storesMu.Unlock()
	return s.db.Close()
}

// migrateLegacy 把历史上的 profiles.json 导入数据库，成功后保留 .bak 备份。
func (s *Store) migrateLegacy(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM profiles").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		var list []Profile
		if err := json.Unmarshal(data, &list); err != nil {
			return fmt.Errorf("解析连接配置失败: %w", err)
		}
		for i := range list {
			for _, field := range sensitiveFields(&list[i]) {
				if secure.IsEncrypted(*field) {
					plain, err := s.keys.Decrypt(*field)
					if err != nil {
						return fmt.Errorf("解密会话「%s」的凭据失败: %w", list[i].Name, err)
					}
					*field = plain
				}
			}
		}
		if _, err := s.Import(list); err != nil {
			return err
		}
	}
	return sqlstore.BackupLegacy(path)
}

func sensitiveFields(p *Profile) []*string {
	return []*string{&p.Password, &p.PrivateKey, &p.Passphrase}
}

func (s *Store) List() []Profile {
	rows, err := s.db.Query("SELECT " + profileColumns + " FROM profiles ORDER BY grp, name")
	if err != nil {
		return []Profile{}
	}
	defer rows.Close()
	out := make([]Profile, 0, 8)
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return []Profile{}
		}
		if err := s.decrypt(&p); err != nil {
			return []Profile{}
		}
		out = append(out, p)
	}
	if rows.Err() != nil {
		return []Profile{}
	}
	return out
}

func (s *Store) Get(id string) (Profile, bool) {
	row := s.db.QueryRow("SELECT "+profileColumns+" FROM profiles WHERE id = ?", id)
	p, err := scanProfile(row)
	if err != nil {
		return Profile{}, false
	}
	if err := s.decrypt(&p); err != nil {
		return Profile{}, false
	}
	return p, true
}

// Save 新增或更新配置，返回保存后的配置。
func (s *Store) Save(p Profile) (Profile, error) {
	isNew := p.ID == ""
	if isNew {
		p.ID = newID()
		p.ApplyDefaults()
	} else if p.Port == 0 {
		p.Port = 22
	}
	args, err := s.profileArgs(&p)
	if err != nil {
		return Profile{}, err
	}
	if _, err := s.db.Exec(upsertProfile, args...); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM profiles WHERE id = ?", id)
	return err
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
	ids := make(map[string]bool, 16)
	rows, err := s.db.Query("SELECT id FROM profiles")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(upsertProfile)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

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
		args, err := s.profileArgs(&p)
		if err != nil {
			return 0, err
		}
		if _, err := stmt.Exec(args...); err != nil {
			return 0, err
		}
		count++
	}
	if count == 0 {
		return 0, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// encrypt 把凭据字段替换为密文，用于写入数据库。
func (s *Store) encrypt(p *Profile) error {
	for _, field := range sensitiveFields(p) {
		encrypted, err := s.keys.Encrypt(*field)
		if err != nil {
			return err
		}
		*field = encrypted
	}
	return nil
}

func (s *Store) decrypt(p *Profile) error {
	for _, field := range sensitiveFields(p) {
		if !secure.IsEncrypted(*field) {
			continue
		}
		plain, err := s.keys.Decrypt(*field)
		if err != nil {
			return fmt.Errorf("解密会话「%s」的凭据失败: %w", p.Name, err)
		}
		*field = plain
	}
	return nil
}

// profileArgs 返回写入数据库的参数，凭据字段已加密。
func (s *Store) profileArgs(p *Profile) ([]any, error) {
	if err := s.encrypt(p); err != nil {
		return nil, err
	}
	return []any{
		p.ID, p.Name, p.Group, p.Description, p.Host, p.Port, p.User, p.Auth,
		p.Password, p.PrivateKey, p.Passphrase, p.JumpProfileID, p.TerminalType,
		p.Encoding, p.StartupCmd, p.KeepAliveSec, b2i(p.LogEnabled), p.FontSize,
		p.ColorScheme, b2i(p.AutoReconnect),
	}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProfile(row rowScanner) (Profile, error) {
	var p Profile
	var logEnabled, autoReconnect int
	err := row.Scan(&p.ID, &p.Name, &p.Group, &p.Description, &p.Host, &p.Port, &p.User, &p.Auth,
		&p.Password, &p.PrivateKey, &p.Passphrase, &p.JumpProfileID, &p.TerminalType,
		&p.Encoding, &p.StartupCmd, &p.KeepAliveSec, &logEnabled, &p.FontSize,
		&p.ColorScheme, &autoReconnect)
	if err != nil {
		return Profile{}, err
	}
	p.LogEnabled = logEnabled != 0
	p.AutoReconnect = autoReconnect != 0
	return p, nil
}

func newID() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}
