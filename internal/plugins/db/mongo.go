package dbapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// mongoConn 保存一个 MongoDB 客户端及其最近使用时间。
type mongoConn struct {
	client   *mongo.Client
	lastUsed time.Time
}

// mongoURI 根据连接配置生成 MongoDB URI。
func mongoURI(c Connection) string {
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	u := url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(host, strconv.Itoa(defPort(c))),
	}
	if c.User != "" {
		u.User = url.UserPassword(c.User, c.Password)
	}
	q := u.Query()
	if mode := strings.ToLower(strings.TrimSpace(c.SSLMode)); mode != "" && mode != "disable" {
		q.Set("tls", "true")
		if mode == "require" {
			q.Set("tlsInsecure", "true")
		}
	}
	for k, v := range parseParams(c.Params) {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// mongoClient 返回（或创建）指定连接的客户端。
func (p *DB) mongoClient(c Connection) (*mongo.Client, error) {
	key := c.ID + "|m"

	p.poolsMu.Lock()
	if pool, ok := p.mongoClients[key]; ok {
		pool.lastUsed = time.Now()
		client := pool.client
		p.poolsMu.Unlock()
		return client, nil
	}
	p.poolsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(mongoURI(c)).
		SetConnectTimeout(8*time.Second).
		SetServerSelectionTimeout(8*time.Second))
	if err != nil {
		return nil, err
	}

	p.poolsMu.Lock()
	if pool, ok := p.mongoClients[key]; ok {
		p.poolsMu.Unlock()
		_ = client.Disconnect(context.Background())
		pool.lastUsed = time.Now()
		return pool.client, nil
	}
	p.mongoClients[key] = &mongoConn{client: client, lastUsed: time.Now()}
	p.poolsMu.Unlock()
	return client, nil
}

// pingMongo 校验连接可用性并返回服务端版本。
func (p *DB) pingMongo(c Connection) (int64, string, error) {
	client, err := p.mongoClient(c)
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	if err := client.Ping(ctx, nil); err != nil {
		return 0, "", err
	}
	latency := time.Since(start).Milliseconds()

	version := ""
	var info bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info); err == nil {
		if v, ok := info["version"].(string); ok && v != "" {
			version = "MongoDB " + v
		}
	}
	return latency, version, nil
}

// handleMongoDatabases 列出数据库名称。
func (p *DB) handleMongoDatabases(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	client, err := p.mongoClient(conn)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
	defer cancel()

	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	sortStrings(names)
	return c.JSON(names)
}

// handleMongoCollections 列出指定数据库下的集合。
func (p *DB) handleMongoCollections(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	database := c.Query("database")
	if database == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少数据库名")
	}
	client, err := p.mongoClient(conn)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
	defer cancel()

	names, err := client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "system.") {
			continue
		}
		out = append(out, name)
	}
	sortStrings(out)
	return c.JSON(out)
}

// parseMongoDoc 将 JSON 文本解析为保持键顺序的 BSON 文档。
func parseMongoDoc(raw, label string) (bson.D, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return bson.D{}, nil
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON([]byte(raw), false, &doc); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 JSON：%w", label, err)
	}
	return doc, nil
}

// handleMongoDocuments 按过滤条件分页查询集合文档。
func (p *DB) handleMongoDocuments(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	database := c.Query("database")
	collection := c.Query("collection")
	if database == "" || collection == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少数据库或集合名")
	}
	filter, err := parseMongoDoc(c.Query("filter"), "过滤条件")
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	sortDoc, err := parseMongoDoc(c.Query("sort"), "排序条件")
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("pageSize", "100"))
	if pageSize < 1 || pageSize > 500 {
		pageSize = 100
	}

	client, err := p.mongoClient(conn)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
	defer cancel()

	coll := client.Database(database).Collection(collection)
	start := time.Now()

	total, countErr := coll.CountDocuments(ctx, filter)
	if countErr != nil {
		total = -1
	}

	opts := options.Find().
		SetSkip(int64((page - 1) * pageSize)).
		SetLimit(int64(pageSize))
	if len(sortDoc) > 0 {
		opts.SetSort(sortDoc)
	}
	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer cur.Close(ctx)

	docs := make([]bson.D, 0, pageSize)
	for cur.Next(ctx) {
		var doc bson.D
		if err := cur.Decode(&doc); err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		docs = append(docs, doc)
	}
	if err := cur.Err(); err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	columns, rows := mongoTable(docs)
	return c.JSON(fiber.Map{
		"columns":    columns,
		"rows":       rows,
		"rowCount":   len(rows),
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"durationMs": time.Since(start).Milliseconds(),
	})
}

// handleMongoCommand 在控制台执行 JSON 形式的数据库命令。
func (p *DB) handleMongoCommand(c *fiber.Ctx) error {
	var in struct {
		ID       string `json:"id"`
		Database string `json:"database"`
		Command  string `json:"command"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	database := strings.TrimSpace(in.Database)
	if database == "" {
		database = strings.TrimSpace(conn.Database)
	}
	if database == "" {
		return jsonErr(c, fiber.StatusBadRequest, "请先选择数据库")
	}
	cmd, err := parseMongoDoc(in.Command, "命令")
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if len(cmd) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "请输入命令，如 {\"find\":\"users\",\"limit\":20}")
	}

	client, err := p.mongoClient(conn)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
	defer cancel()

	start := time.Now()
	var out bson.M
	if err := client.Database(database).RunCommand(ctx, cmd).Decode(&out); err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{
		"ok":         true,
		"result":     mongoValue(out),
		"durationMs": time.Since(start).Milliseconds(),
	})
}

// mongoTable 将文档集合转换为二维表格（列取所有文档键的并集）。
func mongoTable(docs []bson.D) ([]string, [][]any) {
	columns := make([]string, 0, 8)
	seen := make(map[string]bool)
	for _, doc := range docs {
		for _, e := range doc {
			if !seen[e.Key] {
				seen[e.Key] = true
				columns = append(columns, e.Key)
			}
		}
	}
	rows := make([][]any, 0, len(docs))
	for _, doc := range docs {
		byKey := make(map[string]any, len(doc))
		for _, e := range doc {
			byKey[e.Key] = e.Value
		}
		row := make([]any, len(columns))
		for i, name := range columns {
			if v, ok := byKey[name]; ok {
				row[i] = mongoValue(v)
			}
		}
		rows = append(rows, row)
	}
	return columns, rows
}

// mongoValue 将 BSON 值转换为可读的 JSON 友好值。
func mongoValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case primitive.ObjectID:
		return t.Hex()
	case primitive.DateTime:
		return t.Time().Format("2006-01-02 15:04:05.999")
	case primitive.Decimal128:
		return t.String()
	case primitive.Binary:
		return base64.StdEncoding.EncodeToString(t.Data)
	case primitive.Timestamp:
		return fmt.Sprintf("Timestamp(%d, %d)", t.T, t.I)
	case primitive.Regex:
		return "/" + t.Pattern + "/" + t.Options
	case primitive.JavaScript:
		return string(t)
	case primitive.Symbol:
		return string(t)
	case primitive.MinKey, primitive.MaxKey, primitive.Null, primitive.Undefined:
		return nil
	case time.Time:
		return t.Format("2006-01-02 15:04:05.999")
	case bson.D:
		out := make(map[string]any, len(t))
		for _, e := range t {
			out[e.Key] = mongoValue(e.Value)
		}
		return out
	case bson.M:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = mongoValue(val)
		}
		return out
	case bson.A:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = mongoValue(item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = mongoValue(item)
		}
		return out
	case []byte:
		if utf8.Valid(t) {
			return string(t)
		}
		return base64.StdEncoding.EncodeToString(t)
	default:
		return t
	}
}

// sortStrings 就地排序字符串切片（不区分大小写）。
func sortStrings(items []string) {
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i]) < strings.ToLower(items[j])
	})
}
