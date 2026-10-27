// Package vault 提供密钥与配置保险库：分组管理、标签检索、应用解锁密码，
// 全部条目使用主密码派生的密钥加密后落盘。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/scrypt"

	"devos/internal/plugin"
)

//go:embed web
var webFS embed.FS

const (
	storePrefix = "vault:v1:"

	minPasswordRunes = 8
	maxPasswordRunes = 128

	defaultKDFN      = 1 << 15
	defaultKDFR      = 8
	defaultKDFP      = 1
	defaultKeyLength = 32

	sessionTTL = 30 * time.Minute

	maxGroups       = 200
	maxSecrets      = 2000
	maxTags         = 12
	maxTagRunes     = 24
	maxNameRunes    = 120
	maxKeyRunes     = 500
	maxValueRunes   = 12000
	maxNoteRunes    = 2000
	maxGroupRunes   = 60
	maxTotalSecrets = maxSecrets
)

var (
	errCorrupted = errors.New("密钥库数据已损坏")
	groupColors  = map[string]bool{
		"#6366f1": true, "#0ea5e9": true, "#10b981": true, "#f59e0b": true,
		"#f43f5e": true, "#8b5cf6": true, "#14b8a6": true, "#64748b": true,
	}
)

// KDFParams 记录密码派生参数，便于后续升级。
type KDFParams struct {
	N      int `json:"n"`
	R      int `json:"r"`
	P      int `json:"p"`
	KeyLen int `json:"keyLen"`
}

// Meta 是明文落盘的密码元信息：只含盐、校验值与本机参数。
type Meta struct {
	Version   int       `json:"version"`
	Salt      string    `json:"salt"`
	Verifier  string    `json:"verifier"`
	KDF       KDFParams `json:"kdf"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
}

// Group 是一个密钥分组。
type Group struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
	Order int    `json:"order"`
}

// Secret 是一条密钥/配置记录。
type Secret struct {
	ID        string   `json:"id"`
	GroupID   string   `json:"groupId"`
	Name      string   `json:"name"`
	Key       string   `json:"key"`
	Value     string   `json:"value"`
	Note      string   `json:"note"`
	Tags      []string `json:"tags"`
	Order     int      `json:"order"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

// Store 是加密落盘的全部数据。
type Store struct {
	Groups  []Group  `json:"groups"`
	Secrets []Secret `json:"secrets"`
}

// TagCount 用于侧边栏的标签统计。
type TagCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Snapshot 是返回给前端的完整视图。
type Snapshot struct {
	Groups  []Group    `json:"groups"`
	Secrets []Secret   `json:"secrets"`
	Tags    []TagCount `json:"tags"`
}

// API 实现 plugin.Plugin。
type API struct {
	mu    sync.RWMutex
	meta  *Meta
	key   []byte
	store Store

	sessMu   sync.Mutex
	sessions map[string]time.Time

	metaPath  string
	storePath string
	log       *slog.Logger
	kdfN      int
}

// New 创建一个保险库实例。
func New() *API {
	return &API{sessions: map[string]time.Time{}, kdfN: defaultKDFN}
}

func (p *API) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          "vault",
		Name:        "密钥库",
		Description: "按分组管理密钥与配置，支持标签检索与解锁密码，全库加密存储",
		Version:     "1.0.0",
		Category:    "安全",
		Icon:        `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="4"/><path d="M11 11l8.5 8.5M16 16l2-2M19 19l2-2"/></svg>`,
		Accent:      "#6366f1",
		Entry:       "index.html",
		Width:       1180,
		Height:      780,
		Multi:       false,
		Builtin:     true,
		Backend:     true,
	}
}

func (p *API) Init(ctx *plugin.Context) error {
	p.metaPath = filepath.Join(ctx.DataDir, "meta.json")
	p.storePath = filepath.Join(ctx.DataDir, "vault.json")
	p.log = ctx.Logger
	if p.sessions == nil {
		p.sessions = map[string]time.Time{}
	}
	if err := os.MkdirAll(ctx.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建密钥库目录失败: %w", err)
	}
	data, err := os.ReadFile(p.metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取密钥库元信息失败: %w", err)
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("解析密钥库元信息失败: %w", err)
	}
	if meta.Salt == "" || meta.Verifier == "" {
		return errors.New("密钥库元信息不完整")
	}
	p.meta = &meta
	return nil
}

func (p *API) Assets() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func (p *API) Register(api fiber.Router) {
	api.Get("/state", p.handleState)
	api.Post("/setup", p.handleSetup)
	api.Post("/unlock", p.handleUnlock)

	api.Post("/lock", p.requireUnlocked, p.handleLock)
	api.Post("/password", p.requireUnlocked, p.handleChangePassword)
	api.Get("/data", p.requireUnlocked, p.handleData)
	api.Post("/groups", p.requireUnlocked, p.handleCreateGroup)
	api.Put("/groups/:id", p.requireUnlocked, p.handleUpdateGroup)
	api.Delete("/groups/:id", p.requireUnlocked, p.handleDeleteGroup)
	api.Post("/secrets", p.requireUnlocked, p.handleCreateSecret)
	api.Put("/secrets/:id", p.requireUnlocked, p.handleUpdateSecret)
	api.Delete("/secrets/:id", p.requireUnlocked, p.handleDeleteSecret)
}

// ---------- 认证 ----------

func (p *API) requireUnlocked(c *fiber.Ctx) error {
	token := strings.TrimSpace(c.Get("X-Vault-Token"))
	if token == "" || !p.touchSession(token) {
		return fiber.NewError(fiber.StatusUnauthorized, "密钥库已锁定")
	}
	return c.Next()
}

func (p *API) touchSession(token string) bool {
	now := time.Now()
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	p.pruneSessionsLocked(now)
	if expiry, ok := p.sessions[token]; ok && now.Before(expiry) {
		p.sessions[token] = now.Add(sessionTTL)
		return true
	}
	return false
}

func (p *API) pruneSessionsLocked(now time.Time) {
	for token, expiry := range p.sessions {
		if now.After(expiry) {
			delete(p.sessions, token)
		}
	}
}

func (p *API) newSession() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	now := time.Now()
	p.sessMu.Lock()
	p.pruneSessionsLocked(now)
	p.sessions[token] = now.Add(sessionTTL)
	p.sessMu.Unlock()
	return token
}

func (p *API) clearSessions() {
	p.sessMu.Lock()
	p.sessions = map[string]time.Time{}
	p.sessMu.Unlock()
}

func (p *API) unlocked() bool {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	p.pruneSessionsLocked(time.Now())
	return len(p.sessions) > 0
}

// ---------- 状态与解锁 ----------

func (p *API) handleState(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	p.mu.RLock()
	initialized := p.meta != nil
	p.mu.RUnlock()
	return c.JSON(fiber.Map{
		"initialized": initialized,
		"unlocked":    p.unlocked(),
	})
}

type setupInput struct {
	Password  string `json:"password"`
	GroupName string `json:"groupName"`
}

func (p *API) handleSetup(c *fiber.Ctx) error {
	var in setupInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	password, err := validatePassword(in.Password)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.meta != nil {
		return fiber.NewError(fiber.StatusConflict, "密钥库已初始化")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "生成随机盐失败")
	}
	key, err := deriveKey(password, salt, p.kdfN, defaultKDFR, defaultKDFP, defaultKeyLength)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "派生密钥失败")
	}
	now := nowString()
	meta := &Meta{
		Version:   1,
		Salt:      base64.StdEncoding.EncodeToString(salt),
		Verifier:  verifierOf(key),
		KDF:       KDFParams{N: p.kdfN, R: defaultKDFR, P: defaultKDFP, KeyLen: defaultKeyLength},
		CreatedAt: now,
		UpdatedAt: now,
	}
	groupName := normalizeGroupName(in.GroupName)
	if groupName == "" {
		groupName = "Default"
	}
	store := Store{
		Groups:  []Group{{ID: newID(), Name: groupName, Color: "#6366f1", Order: 0}},
		Secrets: []Secret{},
	}
	p.meta = meta
	p.store = store
	p.key = key
	if err := p.persistStoreLocked(); err != nil {
		p.meta, p.store, p.key = nil, Store{}, nil
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if err := p.persistMetaLocked(); err != nil {
		p.meta, p.store, p.key = nil, Store{}, nil
		_ = os.Remove(p.storePath)
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	token := p.newSession()
	return c.JSON(fiber.Map{"token": token, "snapshot": p.snapshotLocked()})
}

type unlockInput struct {
	Password string `json:"password"`
}

func (p *API) handleUnlock(c *fiber.Ctx) error {
	var in unlockInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	p.mu.RLock()
	meta := p.meta
	p.mu.RUnlock()
	if meta == nil {
		return fiber.NewError(fiber.StatusConflict, "密钥库尚未设置密码")
	}
	if runeLen(in.Password) < minPasswordRunes || runeLen(in.Password) > maxPasswordRunes {
		return fiber.NewError(fiber.StatusUnauthorized, "密码不正确")
	}
	salt, err := base64.StdEncoding.DecodeString(meta.Salt)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "密钥库元信息损坏")
	}
	if !validKDF(meta.KDF) {
		return fiber.NewError(fiber.StatusInternalServerError, "密钥库元信息损坏")
	}
	key, err := deriveKey(in.Password, salt, meta.KDF.N, meta.KDF.R, meta.KDF.P, meta.KDF.KeyLen)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "派生密钥失败")
	}
	if subtle.ConstantTimeCompare([]byte(verifierOf(key)), []byte(meta.Verifier)) != 1 {
		return fiber.NewError(fiber.StatusUnauthorized, "密码不正确")
	}

	store, err := p.loadStore(key)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	p.mu.Lock()
	p.store = store
	p.key = key
	if err := p.persistStoreLocked(); err != nil {
		p.mu.Unlock()
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	snapshot := p.snapshotLocked()
	p.mu.Unlock()

	token := p.newSession()
	return c.JSON(fiber.Map{"token": token, "snapshot": snapshot})
}

func (p *API) handleLock(c *fiber.Ctx) error {
	p.clearSessions()
	p.mu.Lock()
	p.key = nil
	p.store = Store{}
	p.mu.Unlock()
	return c.JSON(fiber.Map{"ok": true})
}

type passwordInput struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

func (p *API) handleChangePassword(c *fiber.Ctx) error {
	var in passwordInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	newPassword, err := validatePassword(in.NewPassword)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key == nil || p.meta == nil {
		return fiber.NewError(fiber.StatusUnauthorized, "密钥库已锁定")
	}
	oldSalt, err := base64.StdEncoding.DecodeString(p.meta.Salt)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "密钥库元信息损坏")
	}
	oldKey, err := deriveKey(in.OldPassword, oldSalt, p.meta.KDF.N, p.meta.KDF.R, p.meta.KDF.P, p.meta.KDF.KeyLen)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "派生密钥失败")
	}
	if subtle.ConstantTimeCompare([]byte(verifierOf(oldKey)), []byte(p.meta.Verifier)) != 1 {
		return fiber.NewError(fiber.StatusUnauthorized, "原密码不正确")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "生成随机盐失败")
	}
	key, err := deriveKey(newPassword, salt, p.kdfN, defaultKDFR, defaultKDFP, defaultKeyLength)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "派生密钥失败")
	}
	previous := *p.meta
	p.meta.Salt = base64.StdEncoding.EncodeToString(salt)
	p.meta.Verifier = verifierOf(key)
	p.meta.KDF = KDFParams{N: p.kdfN, R: defaultKDFR, P: defaultKDFP, KeyLen: defaultKeyLength}
	p.meta.UpdatedAt = nowString()
	p.key = key
	if err := p.persistStoreLocked(); err != nil {
		*p.meta = previous
		p.key = oldKey
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if err := p.persistMetaLocked(); err != nil {
		*p.meta = previous
		p.key = oldKey
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	p.clearSessions()
	token := p.newSession()
	return c.JSON(fiber.Map{"token": token, "snapshot": p.snapshotLocked()})
}

// ---------- 数据 ----------

func (p *API) handleData(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	p.mu.RLock()
	defer p.mu.RUnlock()
	return c.JSON(p.snapshotLocked())
}

func (p *API) handleCreateGroup(c *fiber.Ctx) error {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	name := normalizeGroupName(in.Name)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "分组名称不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key == nil {
		return fiber.NewError(fiber.StatusUnauthorized, "密钥库已锁定")
	}
	if len(p.store.Groups) >= maxGroups {
		return fiber.NewError(fiber.StatusBadRequest, "分组数量已达上限")
	}
	order := 0
	for _, group := range p.store.Groups {
		if group.Order >= order {
			order = group.Order + 1
		}
	}
	p.store.Groups = append(p.store.Groups, Group{
		ID:    newID(),
		Name:  name,
		Color: normalizeColor(in.Color),
		Order: order,
	})
	if err := p.persistStoreLocked(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(p.snapshotLocked())
}

func (p *API) handleUpdateGroup(c *fiber.Ctx) error {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	group := p.findGroupLocked(c.Params("id"))
	if group == nil {
		return fiber.NewError(fiber.StatusNotFound, "分组不存在")
	}
	if name := normalizeGroupName(in.Name); name != "" {
		group.Name = name
	}
	if in.Color != "" {
		group.Color = normalizeColor(in.Color)
	}
	if err := p.persistStoreLocked(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(p.snapshotLocked())
}

func (p *API) handleDeleteGroup(c *fiber.Ctx) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	group := p.findGroupLocked(c.Params("id"))
	if group == nil {
		return fiber.NewError(fiber.StatusNotFound, "分组不存在")
	}
	if len(p.store.Groups) <= 1 {
		return fiber.NewError(fiber.StatusBadRequest, "至少需要保留一个分组")
	}
	target := ""
	for _, item := range p.store.Groups {
		if item.ID != group.ID {
			target = item.ID
			break
		}
	}
	if target == "" {
		return fiber.NewError(fiber.StatusBadRequest, "至少需要保留一个分组")
	}
	kept := p.store.Groups[:0]
	for _, item := range p.store.Groups {
		if item.ID != group.ID {
			kept = append(kept, item)
		}
	}
	p.store.Groups = kept
	for i := range p.store.Secrets {
		if p.store.Secrets[i].GroupID == group.ID {
			p.store.Secrets[i].GroupID = target
			p.store.Secrets[i].UpdatedAt = nowString()
		}
	}
	if err := p.persistStoreLocked(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(p.snapshotLocked())
}

type secretInput struct {
	GroupID string   `json:"groupId"`
	Name    string   `json:"name"`
	Key     string   `json:"key"`
	Value   string   `json:"value"`
	Note    string   `json:"note"`
	Tags    []string `json:"tags"`
}

func (p *API) handleCreateSecret(c *fiber.Ctx) error {
	var in secretInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	name := normalizeName(in.Name)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "名称不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key == nil {
		return fiber.NewError(fiber.StatusUnauthorized, "密钥库已锁定")
	}
	if len(p.store.Secrets) >= maxTotalSecrets {
		return fiber.NewError(fiber.StatusBadRequest, "密钥条目已达上限")
	}
	groupID := p.resolveGroupLocked(in.GroupID)
	now := nowString()
	secret := Secret{
		ID:        newID(),
		GroupID:   groupID,
		Name:      name,
		Key:       truncate(in.Key, maxKeyRunes),
		Value:     truncate(in.Value, maxValueRunes),
		Note:      truncate(in.Note, maxNoteRunes),
		Tags:      normalizeTags(in.Tags),
		Order:     p.nextSecretOrderLocked(groupID),
		CreatedAt: now,
		UpdatedAt: now,
	}
	p.store.Secrets = append(p.store.Secrets, secret)
	if err := p.persistStoreLocked(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(p.snapshotLocked())
}

func (p *API) handleUpdateSecret(c *fiber.Ctx) error {
	var in secretInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "参数解析失败")
	}
	name := normalizeName(in.Name)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "名称不能为空")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.findSecretIndexLocked(c.Params("id"))
	if index < 0 {
		return fiber.NewError(fiber.StatusNotFound, "密钥条目不存在")
	}
	secret := &p.store.Secrets[index]
	groupID := p.resolveGroupLocked(in.GroupID)
	if groupID != secret.GroupID {
		secret.GroupID = groupID
		secret.Order = p.nextSecretOrderLocked(groupID)
	}
	secret.Name = name
	secret.Key = truncate(in.Key, maxKeyRunes)
	secret.Value = truncate(in.Value, maxValueRunes)
	secret.Note = truncate(in.Note, maxNoteRunes)
	secret.Tags = normalizeTags(in.Tags)
	secret.UpdatedAt = nowString()
	if err := p.persistStoreLocked(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(p.snapshotLocked())
}

func (p *API) handleDeleteSecret(c *fiber.Ctx) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.findSecretIndexLocked(c.Params("id"))
	if index < 0 {
		return fiber.NewError(fiber.StatusNotFound, "密钥条目不存在")
	}
	p.store.Secrets = append(p.store.Secrets[:index], p.store.Secrets[index+1:]...)
	if err := p.persistStoreLocked(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(p.snapshotLocked())
}

// ---------- 内部辅助 ----------

func (p *API) findGroupLocked(id string) *Group {
	for i := range p.store.Groups {
		if p.store.Groups[i].ID == id {
			return &p.store.Groups[i]
		}
	}
	return nil
}

func (p *API) findSecretIndexLocked(id string) int {
	for i := range p.store.Secrets {
		if p.store.Secrets[i].ID == id {
			return i
		}
	}
	return -1
}

func (p *API) resolveGroupLocked(id string) string {
	if id != "" && p.findGroupLocked(id) != nil {
		return id
	}
	if len(p.store.Groups) == 0 {
		p.store.Groups = append(p.store.Groups, Group{ID: newID(), Name: "Default", Color: "#6366f1"})
	}
	return p.store.Groups[0].ID
}

func (p *API) nextSecretOrderLocked(groupID string) int {
	order := 0
	for _, secret := range p.store.Secrets {
		if secret.GroupID == groupID && secret.Order >= order {
			order = secret.Order + 1
		}
	}
	return order
}

func (p *API) snapshotLocked() Snapshot {
	groups := make([]Group, len(p.store.Groups))
	copy(groups, p.store.Groups)
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Order == groups[j].Order {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].Order < groups[j].Order
	})
	secrets := make([]Secret, len(p.store.Secrets))
	copy(secrets, p.store.Secrets)
	for i := range secrets {
		if secrets[i].Tags == nil {
			secrets[i].Tags = []string{}
		}
	}
	sort.SliceStable(secrets, func(i, j int) bool {
		if secrets[i].UpdatedAt == secrets[j].UpdatedAt {
			return secrets[i].Name < secrets[j].Name
		}
		return secrets[i].UpdatedAt > secrets[j].UpdatedAt
	})
	counts := map[string]int{}
	display := map[string]string{}
	for _, secret := range p.store.Secrets {
		for _, tag := range secret.Tags {
			key := strings.ToLower(tag)
			counts[key]++
			if _, ok := display[key]; !ok {
				display[key] = tag
			}
		}
	}
	tags := make([]TagCount, 0, len(counts))
	for key, count := range counts {
		tags = append(tags, TagCount{Name: display[key], Count: count})
	}
	sort.Slice(tags, func(i, j int) bool {
		return strings.ToLower(tags[i].Name) < strings.ToLower(tags[j].Name)
	})
	return Snapshot{Groups: groups, Secrets: secrets, Tags: tags}
}

func (p *API) loadStore(key []byte) (Store, error) {
	raw, err := os.ReadFile(p.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return p.emptyStore(), nil
		}
		return Store{}, fmt.Errorf("读取密钥库数据失败: %w", err)
	}
	plain, err := openBytes(key, raw)
	if err != nil {
		return Store{}, err
	}
	var store Store
	if err := json.Unmarshal(plain, &store); err != nil {
		return Store{}, errCorrupted
	}
	return p.normalized(store), nil
}

func (p *API) emptyStore() Store {
	return Store{
		Groups:  []Group{{ID: newID(), Name: "Default", Color: "#6366f1"}},
		Secrets: []Secret{},
	}
}

func (p *API) normalized(store Store) Store {
	if store.Groups == nil {
		store.Groups = []Group{}
	}
	if store.Secrets == nil {
		store.Secrets = []Secret{}
	}
	seen := map[string]bool{}
	groups := make([]Group, 0, len(store.Groups))
	for _, group := range store.Groups {
		if !validID(group.ID) || seen[group.ID] {
			group.ID = newID()
		}
		seen[group.ID] = true
		if name := normalizeGroupName(group.Name); name != "" {
			group.Name = name
		} else {
			group.Name = "Default"
		}
		group.Color = normalizeColor(group.Color)
		groups = append(groups, group)
		if len(groups) >= maxGroups {
			break
		}
	}
	if len(groups) == 0 {
		groups = append(groups, Group{ID: newID(), Name: "Default", Color: "#6366f1"})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Order == groups[j].Order {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].Order < groups[j].Order
	})
	for i := range groups {
		groups[i].Order = i
	}
	store.Groups = groups
	groupIDs := map[string]bool{}
	for _, group := range groups {
		groupIDs[group.ID] = true
	}
	fallback := groups[0].ID
	secretSeen := map[string]bool{}
	secrets := make([]Secret, 0, len(store.Secrets))
	for _, secret := range store.Secrets {
		if !validID(secret.ID) || secretSeen[secret.ID] {
			secret.ID = newID()
		}
		secretSeen[secret.ID] = true
		if !groupIDs[secret.GroupID] {
			secret.GroupID = fallback
		}
		if name := normalizeName(secret.Name); name != "" {
			secret.Name = name
		} else {
			secret.Name = "Untitled"
		}
		secret.Key = truncate(secret.Key, maxKeyRunes)
		secret.Value = truncate(secret.Value, maxValueRunes)
		secret.Note = truncate(secret.Note, maxNoteRunes)
		secret.Tags = normalizeTags(secret.Tags)
		if secret.Order < 0 {
			secret.Order = 0
		}
		secrets = append(secrets, secret)
		if len(secrets) >= maxSecrets {
			break
		}
	}
	store.Secrets = secrets
	return store
}

func (p *API) persistStoreLocked() error {
	if p.key == nil {
		return errors.New("密钥库已锁定")
	}
	data, err := json.Marshal(p.store)
	if err != nil {
		return err
	}
	sealed, err := sealBytes(p.key, data)
	if err != nil {
		return err
	}
	return writeAtomic(p.storePath, sealed)
}

func (p *API) persistMetaLocked() error {
	if p.meta == nil {
		return errors.New("缺少密钥库元信息")
	}
	data, err := json.MarshalIndent(p.meta, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p.metaPath, append(data, '\n'))
}

// ---------- 密码与加密 ----------

func validatePassword(password string) (string, error) {
	if runeLen(password) < minPasswordRunes {
		return "", fmt.Errorf("密码至少需要 %d 个字符", minPasswordRunes)
	}
	if runeLen(password) > maxPasswordRunes {
		return "", fmt.Errorf("密码不能超过 %d 个字符", maxPasswordRunes)
	}
	return password, nil
}

func deriveKey(password string, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, n, r, p, keyLen)
}

func validKDF(params KDFParams) bool {
	return params.N >= 1<<12 && params.N <= 1<<20 && params.N&(params.N-1) == 0 &&
		params.R >= 1 && params.R <= 32 &&
		params.P >= 1 && params.P <= 16 &&
		params.KeyLen >= 16 && params.KeyLen <= 64
}

func verifierOf(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])
}

func sealBytes(key, plain []byte) ([]byte, error) {
	gcm, err := gcmOf(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	return []byte(storePrefix + base64.StdEncoding.EncodeToString(sealed)), nil
}

func openBytes(key, raw []byte) ([]byte, error) {
	text := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(text, storePrefix) {
		return nil, errCorrupted
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(text, storePrefix))
	if err != nil {
		return nil, errCorrupted
	}
	gcm, err := gcmOf(key)
	if err != nil {
		return nil, err
	}
	if len(payload) < gcm.NonceSize() {
		return nil, errCorrupted
	}
	plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return nil, errCorrupted
	}
	return plain, nil
}

func gcmOf(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ---------- 校验 ----------

func normalizeName(name string) string {
	return truncate(strings.TrimSpace(name), maxNameRunes)
}

func normalizeGroupName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\n", " "))
	return truncate(name, maxGroupRunes)
}

func normalizeColor(color string) string {
	color = strings.ToLower(strings.TrimSpace(color))
	if groupColors[color] {
		return color
	}
	return "#6366f1"
}

func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = truncate(strings.TrimSpace(tag), maxTagRunes)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
		if len(out) >= maxTags {
			break
		}
	}
	return out
}

func validID(id string) bool {
	if len(id) < 4 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func newID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func nowString() string { return time.Now().Format(time.RFC3339) }

func runeLen(value string) int { return utf8.RuneCountInString(value) }

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vault-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
