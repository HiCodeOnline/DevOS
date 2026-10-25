package dbapp

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "gitee.com/chunanyong/dm" // 达梦 DM 驱动
	"github.com/go-sql-driver/mysql"
	"github.com/gofiber/fiber/v2"
	_ "github.com/jackc/pgx/v5/stdlib"  // PostgreSQL 及兼容数据库
	_ "github.com/microsoft/go-mssqldb" // SQL Server / Azure SQL
	_ "github.com/sijms/go-ora/v2"      // Oracle
	_ "modernc.org/sqlite"              // 纯 Go SQLite 驱动
)

// sqlPool 保存一个连接池及其最近使用时间。
type sqlPool struct {
	db       *sql.DB
	lastUsed time.Time
}

// resolveDatabase 计算连接池使用的数据库/服务名。
// 注意：Oracle 族以 schema 作为树上的“数据库”节点，这里必须保持服务名不变。
func resolveDatabase(c Connection, database string) string {
	switch familyOf(c.Driver) {
	case familySQLite:
		return "main"
	case familyOracle:
		if c.Driver == "dm" {
			return ""
		}
		if c.Database != "" {
			return c.Database
		}
		return "ORCL"
	case familySQLServer:
		if database != "" {
			return database
		}
		return c.Database
	case familyPostgres:
		if database != "" {
			return database
		}
		if c.Database != "" {
			return c.Database
		}
		return "postgres"
	default:
		if database != "" {
			return database
		}
		return c.Database
	}
}

func defPort(c Connection) int {
	port := c.Port
	if port == 0 {
		port = typeOf(c.Driver).DefaultPort
	}
	if port == 0 {
		port = 3306
	}
	return port
}

// buildDSN 根据连接配置生成驱动 DSN。
func buildDSN(c Connection, database string) (string, error) {
	db := resolveDatabase(c, database)
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(defPort(c)))

	switch familyOf(c.Driver) {
	case familySQLite:
		if strings.TrimSpace(c.File) == "" {
			return "", fmt.Errorf("SQLite 数据库文件不能为空")
		}
		return c.File, nil

	case familyPostgres:
		u := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(c.User, c.Password),
			Host:   addr,
			Path:   "/" + db,
		}
		q := u.Query()
		sslmode := c.SSLMode
		if sslmode == "" {
			sslmode = "disable"
		}
		q.Set("sslmode", sslmode)
		q.Set("connect_timeout", "8")
		for k, v := range parseParams(c.Params) {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil

	case familyOracle:
		if c.Driver == "dm" {
			return dmDSN(c, addr), nil
		}
		service := db
		if service == "" {
			service = "ORCL"
		}
		u := url.URL{
			Scheme: "oracle",
			User:   url.UserPassword(c.User, c.Password),
			Host:   addr,
			Path:   "/" + service,
		}
		q := u.Query()
		q.Set("ssl", "false")
		for k, v := range parseParams(c.Params) {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil

	case familySQLServer:
		u := url.URL{
			Scheme: "sqlserver",
			User:   url.UserPassword(c.User, c.Password),
			Host:   addr,
		}
		q := u.Query()
		if db != "" {
			q.Set("database", db)
		}
		q.Set("encrypt", "disable")
		q.Set("dial timeout", "8")
		for k, v := range parseParams(c.Params) {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil

	default:
		cfg := mysql.NewConfig()
		cfg.User = c.User
		cfg.Passwd = c.Password
		cfg.Net = "tcp"
		cfg.Addr = addr
		cfg.DBName = db
		cfg.ParseTime = true
		cfg.Timeout = 8 * time.Second
		cfg.Loc = time.Local
		cfg.Params = map[string]string{}
		if c.Charset != "" {
			cfg.Params["charset"] = c.Charset
		}
		for k, v := range parseParams(c.Params) {
			cfg.Params[k] = v
		}
		return cfg.FormatDSN(), nil
	}
}

// dmDSN 生成达梦驱动的 DSN，数据库字段作为默认模式。
func dmDSN(c Connection, addr string) string {
	var b strings.Builder
	b.WriteString("dm://")
	b.WriteString(c.User)
	if c.Password != "" {
		b.WriteString(":")
		b.WriteString(c.Password)
	}
	b.WriteString("@")
	b.WriteString(addr)

	params := url.Values{}
	if c.Database != "" {
		params.Set("schema", c.Database)
	}
	for k, v := range parseParams(c.Params) {
		params.Set(k, v)
	}
	if len(params) > 0 {
		b.WriteString("?")
		b.WriteString(params.Encode())
	}
	return b.String()
}

// parseParams 解析「k=v&k2=v2」形式的附加参数。
func parseParams(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	raw = strings.TrimPrefix(raw, "?")
	for _, pair := range strings.Split(raw, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			continue
		}
		out[kv[0]] = kv[1]
	}
	return out
}

// pool 返回（或创建）指定连接的连接池。
func (p *DB) pool(c Connection, database string) (*sql.DB, error) {
	key := c.ID + "|" + resolveDatabase(c, database)

	p.poolsMu.Lock()
	if existing, ok := p.pools[key]; ok {
		existing.lastUsed = time.Now()
		db := existing.db
		p.poolsMu.Unlock()
		return db, nil
	}
	p.poolsMu.Unlock()

	dsn, err := buildDSN(c, database)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverImpl(c.Driver), dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	p.poolsMu.Lock()
	if existing, ok := p.pools[key]; ok {
		p.poolsMu.Unlock()
		_ = db.Close()
		existing.lastUsed = time.Now()
		return existing.db, nil
	}
	p.pools[key] = &sqlPool{db: db, lastUsed: time.Now()}
	p.poolsMu.Unlock()
	return db, nil
}

// closePools 关闭某个连接下的全部连接池。
func (p *DB) closePools(connID string) {
	prefix := connID + "|"
	p.poolsMu.Lock()
	defer p.poolsMu.Unlock()
	for key, pool := range p.pools {
		if strings.HasPrefix(key, prefix) {
			_ = pool.db.Close()
			delete(p.pools, key)
		}
	}
}

// versionSQL 返回各协议族的版本查询语句。
func versionSQL(driver string) string {
	switch familyOf(driver) {
	case familyPostgres:
		return "SELECT version()"
	case familySQLite:
		return "SELECT sqlite_version()"
	case familyOracle:
		return "SELECT banner FROM v$version WHERE ROWNUM = 1"
	case familySQLServer:
		return "SELECT @@VERSION"
	default:
		return "SELECT VERSION()"
	}
}

// ping 校验连接可用性并返回版本信息。
func (p *DB) ping(c Connection, database string) (int64, string, error) {
	db, err := p.pool(c, database)
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	if err := db.PingContext(ctx); err != nil {
		return 0, "", err
	}
	latency := time.Since(start).Milliseconds()

	var version string
	_ = db.QueryRowContext(ctx, versionSQL(c.Driver)).Scan(&version)
	if len(version) > 160 {
		version = version[:160]
	}
	return latency, version, nil
}

func (p *DB) handleTest(c *fiber.Ctx) error {
	var in struct {
		ID         string      `json:"id"`
		Connection *Connection `json:"connection"`
		Database   string      `json:"database"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	var conn Connection
	if in.Connection != nil && in.Connection.Driver != "" {
		conn = *in.Connection
		conn.ApplyDefaults()
		if conn.ID == "" {
			conn.ID = "__test__"
		}
		// 临时连接池用完即关，避免残留。
		defer p.closePools(conn.ID)
	} else {
		found, err := p.conn(in.ID)
		if err != nil {
			return jsonErr(c, fiber.StatusBadRequest, err.Error())
		}
		conn = found
	}
	latency, version, err := p.ping(conn, in.Database)
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "latencyMs": latency, "version": version})
}

func (p *DB) handlePing(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	latency, version, err := p.ping(conn, c.Query("database"))
	if err != nil {
		return c.JSON(fiber.Map{"ok": false, "error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true, "latencyMs": latency, "version": version})
}

// Object 描述数据库中的一个表或视图。
type Object struct {
	Name string `json:"name"`
	Type string `json:"type"` // table | view
}

// Column 描述表的一个字段。
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable string `json:"nullable"`
	Key      string `json:"key"`
	Default  string `json:"default"`
	Extra    string `json:"extra"`
	Comment  string `json:"comment"`
}

// sqlLiteral 将字符串转成 SQL 字面量（用于无法参数化的字典查询）。
func sqlLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// oracleOwner 推导 Oracle/达梦字典查询使用的模式名。
func oracleOwner(conn Connection, database, schema string) string {
	owner := database
	if owner == "" {
		owner = schema
	}
	if owner == "" {
		owner = conn.Database
	}
	if owner == "" {
		owner = strings.ToUpper(conn.User)
	}
	return owner
}

func (p *DB) handleDatabases(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	switch familyOf(conn.Driver) {
	case familySQLite:
		return c.JSON([]string{"main"})
	case familyOracle:
		// Oracle/达梦没有“数据库”概念，以模式（用户）作为顶层对象。
		db, err := p.pool(conn, "")
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		rows, err := db.QueryContext(c.Context(), "SELECT username FROM all_users ORDER BY username")
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		defer rows.Close()
		return c.JSON(scanStrings(rows))
	}

	db, err := p.pool(conn, "")
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	var q string
	switch familyOf(conn.Driver) {
	case familySQLServer:
		q = "SELECT name FROM sys.databases WHERE state = 0 ORDER BY name"
	case familyPostgres:
		q = "SELECT datname FROM pg_database WHERE datistemplate = false AND datallowconn ORDER BY 1"
	default:
		q = "SELECT schema_name FROM information_schema.schemata ORDER BY 1"
	}
	rows, err := db.QueryContext(c.Context(), q)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()
	return c.JSON(scanStrings(rows))
}

func scanStrings(rows *sql.Rows) []string {
	out := make([]string, 0, 16)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

func (p *DB) handleSchemas(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	database := c.Query("database")
	switch familyOf(conn.Driver) {
	case familySQLite:
		return c.JSON([]string{"main"})
	case familyMySQL:
		return c.JSON([]string{database})
	case familyOracle:
		return c.JSON([]string{oracleOwner(conn, database, "")})
	}

	db, err := p.pool(conn, database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	var q string
	if familyOf(conn.Driver) == familySQLServer {
		q = "SELECT name FROM sys.schemas WHERE name NOT IN ('sys','INFORMATION_SCHEMA','guest') ORDER BY name"
	} else {
		q = "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast') ORDER BY 1"
	}
	rows, err := db.QueryContext(c.Context(), q)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()
	return c.JSON(scanStrings(rows))
}

func (p *DB) handleObjects(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	database := c.Query("database")
	schema := c.Query("schema")
	db, err := p.pool(conn, database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	var query string
	var args []any
	switch familyOf(conn.Driver) {
	case familySQLite:
		query = "SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name"

	case familyPostgres:
		if schema == "" {
			schema = "public"
		}
		query = "SELECT table_name, table_type FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name"
		args = []any{schema}

	case familyOracle:
		owner := oracleOwner(conn, database, schema)
		query = "SELECT table_name, 'TABLE' FROM all_tables WHERE owner = " + sqlLiteral(owner) +
			" UNION ALL SELECT view_name, 'VIEW' FROM all_views WHERE owner = " + sqlLiteral(owner) +
			" ORDER BY 1"

	case familySQLServer:
		if schema == "" {
			schema = "dbo"
		}
		query = "SELECT TABLE_NAME, TABLE_TYPE FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = @p1 ORDER BY TABLE_NAME"
		args = []any{schema}

	default:
		if database == "" {
			database = conn.Database
		}
		query = "SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME"
		args = []any{database}
	}

	rows, err := db.QueryContext(c.Context(), query, args...)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()

	out := make([]Object, 0, 32)
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			continue
		}
		if strings.Contains(strings.ToUpper(typ), "VIEW") {
			typ = "view"
		} else {
			typ = "table"
		}
		out = append(out, Object{Name: name, Type: typ})
	}
	return c.JSON(out)
}

func (p *DB) handleColumns(c *fiber.Ctx) error {
	conn, err := p.conn(c.Query("id"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	database := c.Query("database")
	schema := c.Query("schema")
	table := c.Query("table")
	if table == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少表名")
	}
	db, err := p.pool(conn, database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	switch familyOf(conn.Driver) {
	case familySQLite:
		rows, err := db.QueryContext(c.Context(), "PRAGMA table_info("+quoteIdent(conn.Driver, table)+")")
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		defer rows.Close()
		out := make([]Column, 0, 16)
		for rows.Next() {
			var (
				cid     int
				name    string
				typ     string
				notNull int
				dflt    sql.NullString
				pk      int
			)
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				continue
			}
			col := Column{Name: name, Type: typ, Nullable: "YES", Default: dflt.String}
			if notNull != 0 {
				col.Nullable = "NO"
			}
			if pk > 0 {
				col.Key = "PRI"
			}
			out = append(out, col)
		}
		return c.JSON(out)

	case familyPostgres:
		if schema == "" {
			schema = "public"
		}
		rows, err := db.QueryContext(c.Context(),
			`SELECT c.column_name, c.data_type, c.is_nullable, COALESCE(c.column_default, '')
			 FROM information_schema.columns c
			 WHERE c.table_schema = $1 AND c.table_name = $2
			 ORDER BY c.ordinal_position`, schema, table)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		defer rows.Close()
		out := make([]Column, 0, 16)
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default); err != nil {
				continue
			}
			out = append(out, col)
		}
		return c.JSON(out)

	case familyOracle:
		owner := oracleOwner(conn, database, schema)
		rows, err := db.QueryContext(c.Context(),
			"SELECT column_name, data_type, nullable, COALESCE(data_default, '') FROM all_tab_columns"+
				" WHERE owner = "+sqlLiteral(owner)+" AND table_name = "+sqlLiteral(table)+
				" ORDER BY column_id")
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		out := make([]Column, 0, 16)
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default); err != nil {
				continue
			}
			out = append(out, col)
		}
		rows.Close()

		// 主键信息为可选增强，权限不足时忽略。
		keys := map[string]bool{}
		keyRows, err := db.QueryContext(c.Context(),
			"SELECT cc.column_name FROM all_constraints c"+
				" JOIN all_cons_columns cc ON c.owner = cc.owner AND c.constraint_name = cc.constraint_name"+
				" WHERE c.constraint_type = 'P' AND c.owner = "+sqlLiteral(owner)+
				" AND c.table_name = "+sqlLiteral(table))
		if err == nil {
			for keyRows.Next() {
				var name string
				if keyRows.Scan(&name) == nil {
					keys[name] = true
				}
			}
			keyRows.Close()
		}
		for i := range out {
			if keys[out[i].Name] {
				out[i].Key = "PRI"
			}
		}
		return c.JSON(out)

	case familySQLServer:
		if schema == "" {
			schema = "dbo"
		}
		rows, err := db.QueryContext(c.Context(),
			`SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, '')
			 FROM INFORMATION_SCHEMA.COLUMNS
			 WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2
			 ORDER BY ORDINAL_POSITION`, schema, table)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		out := make([]Column, 0, 16)
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default); err != nil {
				continue
			}
			out = append(out, col)
		}
		rows.Close()

		keys := map[string]bool{}
		keyRows, err := db.QueryContext(c.Context(),
			`SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
			 JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
			   ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA
			 WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2`, schema, table)
		if err == nil {
			for keyRows.Next() {
				var name string
				if keyRows.Scan(&name) == nil {
					keys[name] = true
				}
			}
			keyRows.Close()
		}
		for i := range out {
			if keys[out[i].Name] {
				out[i].Key = "PRI"
			}
		}
		return c.JSON(out)

	default:
		if database == "" {
			database = conn.Database
		}
		rows, err := db.QueryContext(c.Context(),
			`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COALESCE(COLUMN_DEFAULT, ''), EXTRA, COALESCE(COLUMN_COMMENT, '')
			 FROM information_schema.COLUMNS
			 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			 ORDER BY ORDINAL_POSITION`, database, table)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		defer rows.Close()
		out := make([]Column, 0, 16)
		for rows.Next() {
			var col Column
			if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Key, &col.Default, &col.Extra, &col.Comment); err != nil {
				continue
			}
			out = append(out, col)
		}
		return c.JSON(out)
	}
}

// quoteIdent 按数据库方言转义标识符。
func quoteIdent(driver, name string) string {
	switch familyOf(driver) {
	case familyMySQL:
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case familySQLServer:
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
}

// qualify 生成带库/模式限定的表名。
func qualify(driver, database, schema, table string) string {
	switch familyOf(driver) {
	case familyMySQL:
		if database != "" {
			return quoteIdent(driver, database) + "." + quoteIdent(driver, table)
		}
		return quoteIdent(driver, table)
	case familyPostgres:
		if schema == "" {
			schema = "public"
		}
		return quoteIdent(driver, schema) + "." + quoteIdent(driver, table)
	case familyOracle:
		owner := database
		if owner == "" {
			owner = schema
		}
		if owner != "" {
			return quoteIdent(driver, owner) + "." + quoteIdent(driver, table)
		}
		return quoteIdent(driver, table)
	case familySQLServer:
		if schema == "" {
			schema = "dbo"
		}
		return quoteIdent(driver, schema) + "." + quoteIdent(driver, table)
	default:
		return quoteIdent(driver, table)
	}
}
