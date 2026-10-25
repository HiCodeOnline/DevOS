package dbapp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// redisPool 保存一个 Redis 客户端（按连接 + 库号隔离）及其最近使用时间。
type redisPool struct {
	client   *redis.Client
	lastUsed time.Time
}

// redisOptions 根据连接配置生成客户端选项。
func redisOptions(c Connection, dbIndex int) *redis.Options {
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	opts := &redis.Options{
		Addr:         net.JoinHostPort(host, strconv.Itoa(defPort(c))),
		Username:     c.User,
		Password:     c.Password,
		DB:           dbIndex,
		DialTimeout:  8 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	if mode := strings.ToLower(strings.TrimSpace(c.SSLMode)); mode != "" && mode != "disable" {
		opts.TLSConfig = &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: mode == "require", //nolint:gosec // 用户显式选择 require 时跳过证书校验
		}
	}
	return opts
}

// redisIndex 解析库号，非法值回退为默认库。
func redisIndex(c Connection, db string) int {
	raw := strings.TrimSpace(db)
	if raw == "" {
		raw = strings.TrimSpace(c.Database)
	}
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 0 {
		return 0
	}
	return idx
}

// redisClient 返回（或创建）指定库的客户端。
func (p *DB) redisClient(c Connection, dbIndex int) (*redis.Client, error) {
	key := c.ID + "|r" + strconv.Itoa(dbIndex)

	p.poolsMu.Lock()
	if pool, ok := p.redisClients[key]; ok {
		pool.lastUsed = time.Now()
		client := pool.client
		p.poolsMu.Unlock()
		return client, nil
	}
	p.poolsMu.Unlock()

	client := redis.NewClient(redisOptions(c, dbIndex))

	p.poolsMu.Lock()
	if pool, ok := p.redisClients[key]; ok {
		p.poolsMu.Unlock()
		_ = client.Close()
		pool.lastUsed = time.Now()
		return pool.client, nil
	}
	p.redisClients[key] = &redisPool{client: client, lastUsed: time.Now()}
	p.poolsMu.Unlock()
	return client, nil
}

// pingRedis 校验连接可用性并返回版本。
func (p *DB) pingRedis(c Connection, db string) (int64, string, error) {
	client, err := p.redisClient(c, redisIndex(c, db))
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	if err := client.Ping(ctx).Err(); err != nil {
		return 0, "", err
	}
	latency := time.Since(start).Milliseconds()

	version := ""
	if info, err := client.Info(ctx, "server").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			if strings.HasPrefix(line, "redis_version:") {
				version = "Redis " + strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
				break
			}
		}
	}
	return latency, version, nil
}

// handleRedisDatabases 返回可用的库号列表。
func (p *DB) handleRedisDatabases(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	count := 16
	client, err := p.redisClient(conn, 0)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	if res, err := client.ConfigGet(ctx, "databases").Result(); err == nil {
		if n, err := strconv.Atoi(res["databases"]); err == nil && n > 0 && n <= 1024 {
			count = n
		}
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return c.JSON(out)
}

// RedisKey 描述键空间中一个键的概要。
type RedisKey struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"`
}

// handleRedisKeys 使用 SCAN 分批扫描键并附带类型与 TTL。
func (p *DB) handleRedisKeys(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	client, err := p.redisClient(conn, redisIndex(conn, c.Query("db")))
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	cursor, _ := strconv.ParseUint(c.Query("cursor", "0"), 10, 64)
	pattern := c.Query("pattern", "*")
	if pattern == "" {
		pattern = "*"
	}
	count, _ := strconv.Atoi(c.Query("count", "200"))
	if count <= 0 || count > 500 {
		count = 200
	}

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	keys, next, err := client.Scan(ctx, cursor, pattern, int64(count)).Result()
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	items := make([]RedisKey, 0, len(keys))
	if len(keys) > 0 {
		pipe := client.Pipeline()
		typeCmds := make([]*redis.StatusCmd, len(keys))
		ttlCmds := make([]*redis.DurationCmd, len(keys))
		for i, key := range keys {
			typeCmds[i] = pipe.Type(ctx, key)
			ttlCmds[i] = pipe.TTL(ctx, key)
		}
		_, _ = pipe.Exec(ctx)
		for i, key := range keys {
			typ := "unknown"
			if v, err := typeCmds[i].Result(); err == nil && v != "" {
				typ = v
			}
			ttl := int64(-2)
			if d, err := ttlCmds[i].Result(); err == nil {
				ttl = int64(d / time.Second)
			}
			items = append(items, RedisKey{Key: key, Type: typ, TTL: ttl})
		}
	}
	return c.JSON(fiber.Map{
		"keys":      items,
		"cursor":    next,
		"more":      next != 0,
		"pattern":   pattern,
		"db":        redisIndex(conn, c.Query("db")),
		"truncated": false,
	})
}

// maxRedisText 限制单个字符串值的返回长度。
const maxRedisText = 20000

func clipText(s string) string {
	if len(s) <= maxRedisText {
		return s
	}
	return s[:maxRedisText]
}

// handleRedisKey 返回单个键的类型、TTL 与内容。
func (p *DB) handleRedisKey(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	key := c.Query("key")
	if key == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少键名")
	}
	client, err := p.redisClient(conn, redisIndex(conn, c.Query("db")))
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()

	typ, err := client.Type(ctx, key).Result()
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	if typ == "none" {
		return jsonErr(c, fiber.StatusNotFound, "键不存在或已过期")
	}
	ttl := int64(-1)
	if d, err := client.TTL(ctx, key).Result(); err == nil {
		ttl = int64(d / time.Second)
	}

	resp := fiber.Map{"key": key, "type": typ, "ttl": ttl}
	var length int64 = -1

	switch typ {
	case "string":
		val, err := client.Get(ctx, key).Result()
		if err != nil && err != redis.Nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		length = int64(len(val))
		resp["value"] = clipText(val)
	case "hash":
		val, err := client.HGetAll(ctx, key).Result()
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		length = int64(len(val))
		resp["value"] = val
	case "list":
		val, err := client.LRange(ctx, key, 0, 499).Result()
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		length, _ = client.LLen(ctx, key).Result()
		resp["value"] = val
	case "set":
		val, err := client.SMembers(ctx, key).Result()
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		length, _ = client.SCard(ctx, key).Result()
		resp["value"] = val
	case "zset":
		val, err := client.ZRangeWithScores(ctx, key, 0, 499).Result()
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		length, _ = client.ZCard(ctx, key).Result()
		rows := make([][]any, 0, len(val))
		for _, z := range val {
			rows = append(rows, []any{z.Member, z.Score})
		}
		resp["value"] = rows
	case "stream":
		val, err := client.XRange(ctx, key, "-", "+").Result()
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		length, _ = client.XLen(ctx, key).Result()
		rows := make([]any, 0, len(val))
		for i, msg := range val {
			if i >= 200 {
				break
			}
			rows = append(rows, map[string]any{"id": msg.ID, "values": msg.Values})
		}
		resp["value"] = rows
	default:
		resp["value"] = nil
		resp["unsupported"] = true
	}
	resp["length"] = length
	return c.JSON(resp)
}

// handleRedisDeleteKey 删除指定键。
func (p *DB) handleRedisDeleteKey(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	key := c.Query("key")
	if key == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少键名")
	}
	client, err := p.redisClient(conn, redisIndex(conn, c.Query("db")))
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	n, err := client.Del(c.Context(), key).Result()
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"ok": n > 0, "deleted": n})
}

// redisCommandResults 保存一行命令的执行结果。
type redisCommandResult struct {
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Value   any    `json:"value,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleRedisCommand 在控制台逐行执行 Redis 命令。
func (p *DB) handleRedisCommand(c *fiber.Ctx) error {
	var in struct {
		ID      string `json:"id"`
		DB      string `json:"db"`
		Command string `json:"command"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	lines := strings.Split(in.Command, "\n")
	if strings.TrimSpace(in.Command) == "" {
		return jsonErr(c, fiber.StatusBadRequest, "请输入 Redis 命令")
	}
	client, err := p.redisClient(conn, redisIndex(conn, in.DB))
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
	defer cancel()

	start := time.Now()
	results := make([]redisCommandResult, 0, len(lines))
	for _, line := range lines {
		args := splitRedisCommand(line)
		if len(args) == 0 {
			continue
		}
		entry := redisCommandResult{Command: strings.Join(args, " ")}
		val, execErr := client.Do(ctx, toAnySlice(args)...).Result()
		if execErr != nil && execErr != redis.Nil {
			entry.Error = execErr.Error()
		} else {
			entry.OK = true
			entry.Value = redisValue(val)
		}
		results = append(results, entry)
	}
	return c.JSON(fiber.Map{
		"results":    results,
		"durationMs": time.Since(start).Milliseconds(),
	})
}

// splitRedisCommand 按 shell 风格拆分命令，支持单双引号。
func splitRedisCommand(line string) []string {
	var (
		args    []string
		buf     strings.Builder
		single  bool
		double  bool
		escaped bool
		started bool
	)
	for _, r := range line {
		switch {
		case escaped:
			buf.WriteRune(r)
			escaped = false
			started = true
		case double:
			if r == '\\' {
				escaped = true
			} else if r == '"' {
				double = false
			} else {
				buf.WriteRune(r)
			}
		case single:
			if r == '\'' {
				single = false
			} else {
				buf.WriteRune(r)
			}
		case r == '"':
			double = true
			started = true
		case r == '\'':
			single = true
			started = true
		case unicode.IsSpace(r):
			if started {
				args = append(args, buf.String())
				buf.Reset()
				started = false
			}
		default:
			buf.WriteRune(r)
			started = true
		}
	}
	if started {
		args = append(args, buf.String())
	}
	return args
}

// toAnySlice 将字符串参数转换为 go-redis 需要的类型。
func toAnySlice(args []string) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}

// redisValue 将 Redis 回复转换为可 JSON 序列化的值。
func redisValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string, int, int64, uint64, float64, bool:
		return t
	case []byte:
		return string(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = redisValue(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = redisValue(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = redisValue(val)
		}
		return out
	case error:
		return t.Error()
	default:
		return fmt.Sprint(t)
	}
}
