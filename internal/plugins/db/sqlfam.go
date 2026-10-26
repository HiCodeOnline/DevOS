package dbapp

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// 本文件集中处理通过 database/sql 接入的新协议族：
// Firebird、SAP HANA、Exasol、Vertica、Trino/Presto、Snowflake、Hive 系。

// extraDSN 生成新协议族的 DSN；第二个返回值表示是否命中。
func extraDSN(c Connection, addr, db string) (string, bool, error) {
	params := parseParams(c.Params)
	apply := func(u *url.URL) {
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	switch familyOf(c.Driver) {
	case familyFirebird:
		if strings.TrimSpace(db) == "" {
			return "", true, fmt.Errorf("Firebird 需要填写数据库文件或别名")
		}
		charset := "UTF8"
		if v := params["charset"]; v != "" {
			charset = v
		}
		dsn := escapeUserinfo(c.User) + ":" + escapeUserinfo(c.Password) + "@" + addr + "/" +
			escapeDSPath(db) + "?charset=" + url.QueryEscape(charset)
		return dsn, true, nil

	case familyHana:
		u := &url.URL{Scheme: "hdb", Host: addr}
		if c.User != "" {
			u.User = url.UserPassword(c.User, c.Password)
		}
		q := u.Query()
		if db != "" {
			// go-hdb 通过 databaseName 参数选择租户库，路径会被忽略。
			q.Set("databaseName", db)
		}
		u.RawQuery = q.Encode()
		apply(u)
		return u.String(), true, nil

	case familyExasol:
		u := &url.URL{Scheme: "exasol", Host: addr}
		if c.User != "" {
			u.User = url.UserPassword(c.User, c.Password)
		}
		q := u.Query()
		q.Set("validateservercertificate", "0")
		u.RawQuery = q.Encode()
		apply(u)
		return u.String(), true, nil

	case familyVertica:
		u := &url.URL{Scheme: "vertica", Host: addr}
		if c.User != "" {
			u.User = url.UserPassword(c.User, c.Password)
		}
		if db != "" {
			u.Path = "/" + db
		}
		apply(u)
		return u.String(), true, nil

	case familyTrino:
		scheme := "http"
		if v := params["scheme"]; v != "" {
			scheme = v
		} else if defPort(c) == 443 {
			scheme = "https"
		}
		u := &url.URL{Scheme: scheme, Host: addr}
		if c.User != "" {
			u.User = url.UserPassword(c.User, c.Password)
		}
		q := u.Query()
		catalog := strings.TrimSpace(db)
		if catalog == "" {
			catalog = "system"
		}
		q.Set("catalog", catalog)
		if schema := strings.TrimSpace(params["defaultSchema"]); schema != "" {
			q.Set("schema", schema)
		}
		u.RawQuery = q.Encode()
		apply(u)
		return u.String(), true, nil

	case familySnowflake:
		account := strings.TrimSpace(params["account"])
		host := strings.TrimSpace(c.Host)
		if account == "" {
			if host == "" {
				return "", true, fmt.Errorf("Snowflake 需要在附加参数中提供 account")
			}
			// 账号通常就是主机名的第一段，如 myorg-xy12345.snowflakecomputing.com。
			account = strings.Split(host, ".")[0]
		}
		q := url.Values{}
		q.Set("account", account)
		if w := params["warehouse"]; w != "" {
			q.Set("warehouse", w)
		}
		if role := params["role"]; role != "" {
			q.Set("role", role)
		}
		for k, v := range params {
			if k != "account" && k != "warehouse" && k != "role" {
				q.Set(k, v)
			}
		}
		// gosnowflake 的 DSN 形态为 user:pass@host:port/database?account=...，
		// 不能带 scheme，否则会被误解析为路径分隔符。
		dsn := escapeUserinfo(c.User) + ":" + escapeUserinfo(c.Password) + "@" + addr + "/" + escapeDSPath(db)
		if encoded := q.Encode(); encoded != "" {
			dsn += "?" + encoded
		}
		return dsn, true, nil

	case familyHive:
		u := &url.URL{Scheme: "hive", Host: addr}
		if c.User != "" {
			u.User = url.UserPassword(c.User, c.Password)
		}
		if db != "" {
			u.Path = "/" + db
		}
		q := u.Query()
		if v := params["auth"]; v != "" {
			q.Set("auth", v)
		}
		if v := params["transport"]; v != "" {
			q.Set("transport", v)
		}
		if v := params["fetchSize"]; v != "" {
			q.Set("fetchSize", v)
		}
		u.RawQuery = q.Encode()
		return u.String(), true, nil
	}
	return "", false, nil
}

// escapeUserinfo 转义 DSN 用户信息中的保留字符。
func escapeUserinfo(s string) string {
	replacer := strings.NewReplacer("@", "%40", ":", "%3A", "/", "%2F", "?", "%3F", "#", "%23", "&", "%26", "=", "%3D", " ", "%20")
	return replacer.Replace(s)
}

// escapeDSPath 转义 Firebird 数据库路径中的保留字符，保留路径分隔符。
func escapeDSPath(s string) string {
	replacer := strings.NewReplacer("@", "%40", ":", "%3A", "?", "%3F", "#", "%23", "&", "%26", "=", "%3D", " ", "%20", "%", "%25")
	return replacer.Replace(s)
}

// extraVersionSQL 返回新协议族的版本查询语句。
func extraVersionSQL(driver string) (string, bool) {
	switch familyOf(driver) {
	case familyFirebird:
		return "SELECT rdb$get_context('SYSTEM', 'ENGINE_VERSION') FROM rdb$database", true
	case familyHana:
		return "SELECT VERSION FROM SYS.M_DATABASE", true
	case familyExasol:
		return "SELECT PARAM_VALUE FROM SYS.EXA_METADATA WHERE PARAM_NAME = 'databaseProductVersion'", true
	case familyVertica:
		return "SELECT version()", true
	case familyTrino, familySnowflake, familyHive:
		return "SELECT version()", true
	}
	return "", false
}

// extraDatabases 查询新协议族的命名空间（库/模式）列表。
func extraDatabases(ctx context.Context, db *sql.DB, conn Connection, database string) ([]string, error) {
	switch familyOf(conn.Driver) {
	case familyFirebird:
		return []string{"main"}, nil
	case familyHana:
		return queryStrings(ctx, db,
			`SELECT SCHEMA_NAME FROM SYS.SCHEMAS
			 WHERE SCHEMA_NAME NOT LIKE 'SYS%' AND SCHEMA_NAME <> '_SYS_BIC'
			 ORDER BY SCHEMA_NAME`)
	case familyExasol:
		return queryStrings(ctx, db,
			`SELECT SCHEMA_NAME FROM SYS.EXA_SCHEMAS
			 WHERE SCHEMA_NAME NOT IN ('SYS','EXA_STATISTICS')
			 ORDER BY SCHEMA_NAME`)
	case familyVertica:
		return queryStrings(ctx, db,
			`SELECT schema_name FROM v_catalog.schemata
			 WHERE schema_name NOT IN ('v_catalog','v_monitor','v_internal','v_func','information_schema')
			 ORDER BY schema_name`)
	case familyTrino:
		return queryStrings(ctx, db, "SHOW CATALOGS")
	case familySnowflake:
		if database != "" {
			return []string{database}, nil
		}
		return queryStrings(ctx, db,
			`SELECT DATABASE_NAME FROM SNOWFLAKE.INFORMATION_SCHEMA.DATABASES
			 WHERE DATABASE_NAME <> 'SNOWFLAKE' ORDER BY DATABASE_NAME`)
	case familyHive:
		return queryStrings(ctx, db, "SHOW DATABASES")
	}
	return nil, nil
}

// extraSchemas 查询新协议族下的模式列表。
func extraSchemas(ctx context.Context, db *sql.DB, conn Connection, database string) ([]string, error) {
	switch familyOf(conn.Driver) {
	case familyTrino:
		catalog := database
		if catalog == "" {
			catalog = conn.Database
		}
		if catalog == "" {
			catalog = "system"
		}
		return queryStrings(ctx, db, `SELECT schema_name FROM `+quoteIdent(conn.Driver, catalog)+
			`.information_schema.schemata ORDER BY schema_name`)
	case familySnowflake:
		database := database
		if database == "" {
			database = conn.Database
		}
		if database == "" {
			return nil, nil
		}
		return queryStrings(ctx, db, `SELECT SCHEMA_NAME FROM `+quoteIdent(conn.Driver, database)+
			`.INFORMATION_SCHEMA.SCHEMATA ORDER BY SCHEMA_NAME`)
	}
	return nil, nil
}

// extraObjects 查询新协议族下的表与视图。
func extraObjects(ctx context.Context, db *sql.DB, conn Connection, database, schema string) ([]Object, error) {
	switch familyOf(conn.Driver) {
	case familyFirebird:
		rows, err := db.QueryContext(ctx,
			`SELECT TRIM(RDB$RELATION_NAME), CASE WHEN RDB$VIEW_BLR IS NULL THEN 'TABLE' ELSE 'VIEW' END
			 FROM RDB$RELATIONS WHERE RDB$SYSTEM_FLAG = 0 ORDER BY RDB$RELATION_NAME`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjects(rows)

	case familyHana:
		owner := schema
		if owner == "" {
			owner = database
		}
		if owner == "" {
			owner = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT TABLE_NAME, TABLE_TYPE FROM SYS.TABLES WHERE SCHEMA_NAME = ? ORDER BY TABLE_NAME`, owner)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjects(rows)

	case familyExasol:
		owner := schema
		if owner == "" {
			owner = database
		}
		if owner == "" {
			owner = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT TABLE_NAME, TABLE_TYPE FROM SYS.EXA_ALL_TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, owner)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjects(rows)

	case familyVertica:
		owner := schema
		if owner == "" {
			owner = database
		}
		if owner == "" {
			owner = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT table_name, table_type FROM v_catalog.tables WHERE table_schema = ?
			 UNION ALL SELECT table_name, 'VIEW' FROM v_catalog.views WHERE table_schema = ?
			 ORDER BY 1`, owner, owner)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjects(rows)

	case familyTrino:
		catalog := database
		if catalog == "" {
			catalog = conn.Database
		}
		if catalog == "" {
			catalog = "system"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT table_name, table_type FROM `+quoteIdent(conn.Driver, catalog)+
				`.information_schema.tables WHERE table_schema = ? ORDER BY table_name`, schema)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjects(rows)

	case familySnowflake:
		database := database
		if database == "" {
			database = conn.Database
		}
		if database == "" {
			return nil, fmt.Errorf("请先选择数据库")
		}
		rows, err := db.QueryContext(ctx,
			`SELECT TABLE_NAME, TABLE_TYPE FROM `+quoteIdent(conn.Driver, database)+
				`.INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, schema)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanObjects(rows)

	case familyHive:
		q := "SHOW TABLES"
		if database != "" {
			q = "SHOW TABLES IN " + quoteIdent(conn.Driver, database)
		}
		names, err := queryStrings(ctx, db, q)
		if err != nil {
			return nil, err
		}
		out := make([]Object, 0, len(names))
		for _, name := range names {
			out = append(out, Object{Name: name, Type: "table"})
		}
		return out, nil
	}
	return nil, nil
}

func scanObjects(rows *sql.Rows) ([]Object, error) {
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
	return out, rows.Err()
}

// extraColumns 查询新协议族下表的字段列表。
func extraColumns(ctx context.Context, db *sql.DB, conn Connection, database, schema, table string) ([]Column, error) {
	switch familyOf(conn.Driver) {
	case familyFirebird:
		rows, err := db.QueryContext(ctx,
			`SELECT TRIM(rf.RDB$FIELD_NAME),
			        CASE f.RDB$FIELD_TYPE
			          WHEN 7 THEN 'SMALLINT' WHEN 8 THEN 'INTEGER' WHEN 10 THEN 'FLOAT'
			          WHEN 12 THEN 'DATE' WHEN 13 THEN 'TIME' WHEN 14 THEN 'CHAR'
			          WHEN 16 THEN 'BIGINT' WHEN 27 THEN 'DOUBLE' WHEN 35 THEN 'TIMESTAMP'
			          WHEN 37 THEN 'VARCHAR' WHEN 261 THEN 'BLOB'
			          ELSE 'TYPE_' || f.RDB$FIELD_TYPE END,
			        CASE WHEN rf.RDB$NULL_FLAG = 1 THEN 'NO' ELSE 'YES' END,
			        COALESCE(CAST(rf.RDB$DEFAULT_SOURCE AS VARCHAR(200)), '')
			 FROM RDB$RELATION_FIELDS rf
			 JOIN RDB$FIELDS f ON rf.RDB$FIELD_SOURCE = f.RDB$FIELD_NAME
			 WHERE rf.RDB$RELATION_NAME = ? ORDER BY rf.RDB$FIELD_POSITION`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanColumns(rows, 4)

	case familyHana:
		owner := schema
		if owner == "" {
			owner = database
		}
		if owner == "" {
			owner = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT COLUMN_NAME, DATA_TYPE_NAME, IS_NULLABLE, COALESCE(CAST(DEFAULT_VALUE AS NVARCHAR(200)), '')
			 FROM SYS.TABLE_COLUMNS WHERE SCHEMA_NAME = ? AND TABLE_NAME = ? ORDER BY POSITION`, owner, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanColumns(rows, 4)

	case familyExasol:
		owner := schema
		if owner == "" {
			owner = database
		}
		if owner == "" {
			owner = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, '')
			 FROM SYS.EXA_ALL_COLUMNS WHERE COLUMN_SCHEMA = ? AND COLUMN_TABLE = ? ORDER BY COLUMN_NAME`, owner, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanColumns(rows, 4)

	case familyVertica:
		owner := schema
		if owner == "" {
			owner = database
		}
		if owner == "" {
			owner = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
			 FROM v_catalog.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, owner, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanColumns(rows, 4)

	case familyTrino:
		catalog := database
		if catalog == "" {
			catalog = conn.Database
		}
		if catalog == "" {
			catalog = "system"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
			 FROM `+quoteIdent(conn.Driver, catalog)+
				`.information_schema.columns WHERE table_schema = ? AND table_name = ?
			 ORDER BY ordinal_position`, schema, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanColumns(rows, 4)

	case familySnowflake:
		database := database
		if database == "" {
			database = conn.Database
		}
		if database == "" {
			return nil, fmt.Errorf("请先选择数据库")
		}
		rows, err := db.QueryContext(ctx,
			`SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COALESCE(COLUMN_DEFAULT, '')
			 FROM `+quoteIdent(conn.Driver, database)+
				`.INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			 ORDER BY ORDINAL_POSITION`, schema, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanColumns(rows, 4)

	case familyHive:
		rows, err := db.QueryContext(ctx, "DESCRIBE "+quoteIdent(conn.Driver, table))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]Column, 0, 16)
		for rows.Next() {
			values := make([]any, 3)
			ptrs := make([]any, 3)
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			name := strings.TrimSpace(fmt.Sprint(values[0]))
			typ := strings.TrimSpace(fmt.Sprint(values[1]))
			if name == "" || strings.HasPrefix(name, "#") || typ == "" {
				continue
			}
			out = append(out, Column{Name: name, Type: typ, Nullable: "YES",
				Comment: strings.TrimSpace(fmt.Sprint(values[2]))})
		}
		return out, rows.Err()
	}
	return nil, nil
}

// scanColumns 读取「名称、类型、可空、默认值」四列结构。
func scanColumns(rows *sql.Rows, width int) ([]Column, error) {
	out := make([]Column, 0, 16)
	for rows.Next() {
		var (
			name     string
			typ      sql.NullString
			nullable sql.NullString
			dflt     sql.NullString
		)
		if err := rows.Scan(&name, &typ, &nullable, &dflt); err != nil {
			continue
		}
		out = append(out, Column{
			Name:     name,
			Type:     typ.String,
			Nullable: strings.ToUpper(nullable.String),
			Default:  dflt.String,
		})
	}
	return out, rows.Err()
}

// queryStrings 读取单列结果集。
func queryStrings(ctx context.Context, db *sql.DB, q string) ([]string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows), nil
}

// firebirdTableQuery 生成 Firebird 的 ROWS 分页片段。
func firebirdPage(page, pageSize, offset int) string {
	start := offset + 1
	end := offset + pageSize
	return " ROWS " + strconv.Itoa(start) + " TO " + strconv.Itoa(end)
}
