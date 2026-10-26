package dbapp

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Index 描述表上的一个索引。
type Index struct {
	Name       string   `json:"name"`
	Unique     bool     `json:"unique"`
	Primary    bool     `json:"primary"`
	Columns    []string `json:"columns"`
	Definition string   `json:"definition,omitempty"`
}

// ForeignKey 描述表上的一个外键。
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
}

// placeholder 返回指定方言的第 i 个参数占位符。
func placeholder(driver string, i int) string {
	switch familyOf(driver) {
	case familyPostgres:
		return "$" + strconv.Itoa(i)
	case familySQLServer:
		return "@p" + strconv.Itoa(i)
	case familyOracle:
		return ":" + strconv.Itoa(i)
	default:
		return "?"
	}
}

// toDBValue 将 JSON 解码出的数值转换为驱动更适合的类型。
func toDBValue(v any) any {
	switch t := v.(type) {
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return int64(t)
		}
		return t
	default:
		return v
	}
}

func (p *DB) writeAllowed(conn Connection) error {
	if conn.ReadOnly {
		return fmt.Errorf("该连接为只读模式，已拒绝执行写操作")
	}
	return nil
}

// connInfo 解析请求中通用的连接与表定位参数。
func (p *DB) connInfo(c *fiber.Ctx) (Connection, error) {
	return p.conn(c.Query("id"))
}

// safeType 校验用户在结构编辑中输入的列类型，禁止语句拼接。
func safeType(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", fmt.Errorf("列类型不能为空")
	}
	if strings.ContainsAny(t, ";") || strings.Contains(t, "--") || strings.Contains(t, "/*") {
		return "", fmt.Errorf("列类型包含非法字符")
	}
	return t, nil
}

/* ---------------- 表数据增删改 ---------------- */

type rowPayload struct {
	ID       string           `json:"id"`
	Database string           `json:"database"`
	Schema   string           `json:"schema"`
	Table    string           `json:"table"`
	Action   string           `json:"action"` // insert | update | delete
	PK       map[string]any   `json:"pk"`
	Values   map[string]any   `json:"values"`
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
}

func (p *DB) handleTableRows(c *fiber.Ctx) error {
	var in rowPayload
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if err := p.writeAllowed(conn); err != nil {
		return jsonErr(c, fiber.StatusForbidden, err.Error())
	}
	if strings.TrimSpace(in.Table) == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少表名")
	}
	db, err := p.pool(conn, in.Database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	qualified := qualify(conn.Driver, in.Database, in.Schema, in.Table)

	switch strings.ToLower(in.Action) {
	case "insert":
		return p.insertRows(ctx, c, conn, db, qualified, in)
	case "update":
		return p.updateRow(ctx, c, conn, db, qualified, in)
	case "delete":
		return p.deleteRow(ctx, c, conn, db, qualified, in)
	default:
		return jsonErr(c, fiber.StatusBadRequest, "不支持的操作类型")
	}
}

func (p *DB) insertRows(ctx context.Context, c *fiber.Ctx, conn Connection, db *sql.DB, qualified string, in rowPayload) error {
	rows := in.Rows
	if len(rows) == 0 && in.Values != nil {
		rows = []map[string]any{in.Values}
	}
	if len(rows) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "没有待插入的数据")
	}
	columns := in.Columns
	if len(columns) == 0 {
		for k := range rows[0] {
			columns = append(columns, k)
		}
	}
	if len(columns) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "没有待插入的列")
	}

	quoted := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = quoteIdent(conn.Driver, col)
	}
	var placeholders []string
	for i := range columns {
		placeholders = append(placeholders, placeholder(conn.Driver, i+1))
	}
	stmt := "INSERT INTO " + qualified + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"

	exec := func(e interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}) (int64, error) {
		var affected int64
		for _, row := range rows {
			args := make([]any, len(columns))
			for i, col := range columns {
				args[i] = toDBValue(row[col])
			}
			res, err := e.ExecContext(ctx, stmt, args...)
			if err != nil {
				return affected, err
			}
			n, _ := res.RowsAffected()
			affected += n
		}
		return affected, nil
	}

	var (
		affected int64
		err      error
	)
	if familyOf(conn.Driver) == familyClickHouse {
		affected, err = exec(db)
	} else {
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			affected, err = exec(db)
		} else {
			affected, err = exec(tx)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
	}
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true, "affected": affected, "inserted": len(rows)})
}

func (p *DB) updateRow(ctx context.Context, c *fiber.Ctx, conn Connection, db *sql.DB, qualified string, in rowPayload) error {
	if len(in.PK) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "缺少主键，已拒绝更新以避免误改全表")
	}
	if len(in.Values) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "没有待更新的字段")
	}
	keys := sortedKeys(in.Values)
	sets := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)+len(in.PK))
	for i, col := range keys {
		sets = append(sets, quoteIdent(conn.Driver, col)+" = "+placeholder(conn.Driver, i+1))
		args = append(args, toDBValue(in.Values[col]))
	}
	where, whereArgs := buildWhere(conn.Driver, in.PK, len(keys))
	args = append(args, whereArgs...)
	stmt := "UPDATE " + qualified + " SET " + strings.Join(sets, ", ") + " WHERE " + where

	tx, txErr := db.BeginTx(ctx, nil)
	if familyOf(conn.Driver) == familyClickHouse || txErr != nil {
		res, err := db.ExecContext(ctx, stmt, args...)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		n, _ := res.RowsAffected()
		return c.JSON(fiber.Map{"ok": true, "affected": n})
	}
	res, err := tx.ExecContext(ctx, stmt, args...)
	if err != nil {
		_ = tx.Rollback()
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true, "affected": n})
}

func (p *DB) deleteRow(ctx context.Context, c *fiber.Ctx, conn Connection, db *sql.DB, qualified string, in rowPayload) error {
	if len(in.PK) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "缺少主键，已拒绝删除以避免误删全表")
	}
	where, args := buildWhere(conn.Driver, in.PK, 0)
	stmt := "DELETE FROM " + qualified + " WHERE " + where

	tx, txErr := db.BeginTx(ctx, nil)
	if familyOf(conn.Driver) == familyClickHouse || txErr != nil {
		res, err := db.ExecContext(ctx, stmt, args...)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		n, _ := res.RowsAffected()
		return c.JSON(fiber.Map{"ok": true, "affected": n})
	}
	res, err := tx.ExecContext(ctx, stmt, args...)
	if err != nil {
		_ = tx.Rollback()
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"ok": true, "affected": n})
}

func buildWhere(driver string, pk map[string]any, offset int) (string, []any) {
	keys := sortedKeys(pk)
	parts := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for i, col := range keys {
		parts = append(parts, quoteIdent(driver, col)+" = "+placeholder(driver, offset+i+1))
		args = append(args, toDBValue(pk[col]))
	}
	return strings.Join(parts, " AND "), args
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

/* ---------------- 表结构读取与编辑 ---------------- */

func (p *DB) handleTableStructure(c *fiber.Ctx) error {
	conn, err := p.connInfo(c)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	table := c.Query("table")
	if table == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少表名")
	}
	db, err := p.pool(conn, c.Query("database"))
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cols, err := p.loadTableColumns(ctx, conn, db, c.Query("database"), c.Query("schema"), table)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	indexes, fks := p.loadIndexes(ctx, conn, db, c.Query("database"), c.Query("schema"), table)
	return c.JSON(fiber.Map{"columns": cols, "indexes": indexes, "foreignKeys": fks})
}

type alterOp struct {
	Kind    string   `json:"kind"` // add | drop | modify | rename | addIndex | dropIndex
	Name    string   `json:"name"`
	NewName string   `json:"newName"`
	Type    string   `json:"type"`
	NotNull bool     `json:"notNull"`
	Default string   `json:"default"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

func (p *DB) handleTableAlter(c *fiber.Ctx) error {
	var in struct {
		ID       string    `json:"id"`
		Database string    `json:"database"`
		Schema   string    `json:"schema"`
		Table    string    `json:"table"`
		Ops      []alterOp `json:"ops"`
		Preview  bool      `json:"preview"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if strings.TrimSpace(in.Table) == "" {
		return jsonErr(c, fiber.StatusBadRequest, "缺少表名")
	}
	if len(in.Ops) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "没有待执行的结构变更")
	}
	statements, err := buildAlterStatements(conn.Driver, in.Table, in.Ops)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if in.Preview {
		return c.JSON(fiber.Map{"statements": statements})
	}
	if err := p.writeAllowed(conn); err != nil {
		return jsonErr(c, fiber.StatusForbidden, err.Error())
	}
	db, err := p.pool(conn, in.Database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	results := make([]fiber.Map, 0, len(statements))
	for _, stmt := range statements {
		start := time.Now()
		_, execErr := db.ExecContext(ctx, stmt)
		item := fiber.Map{"statement": stmt, "durationMs": time.Since(start).Milliseconds()}
		if execErr != nil {
			item["error"] = execErr.Error()
			results = append(results, item)
			break
		}
		results = append(results, item)
	}
	return c.JSON(fiber.Map{"results": results})
}

func buildAlterStatements(driver, table string, ops []alterOp) ([]string, error) {
	fam := familyOf(driver)
	qTable := quoteIdent(driver, table)
	if fam == familySQLite {
		return buildSQLiteAlter(qTable, ops)
	}
	var out []string
	for _, op := range ops {
		name := strings.TrimSpace(op.Name)
		needsName := op.Kind != "addIndex"
		if needsName && name == "" {
			return nil, fmt.Errorf("列名不能为空")
		}
		qName := quoteIdent(driver, name)
		switch op.Kind {
		case "add":
			typ, err := safeType(op.Type)
			if err != nil {
				return nil, err
			}
			def := ""
			if strings.TrimSpace(op.Default) != "" {
				def = " DEFAULT " + strings.TrimSpace(op.Default)
			}
			notNull := ""
			if op.NotNull {
				notNull = " NOT NULL"
			}
			switch fam {
			case familySQLServer:
				out = append(out, "ALTER TABLE "+qTable+" ADD "+qName+" "+typ+def+notNull)
			case familyOracle:
				out = append(out, "ALTER TABLE "+qTable+" ADD ("+qName+" "+typ+def+notNull+")")
			default:
				out = append(out, "ALTER TABLE "+qTable+" ADD COLUMN "+qName+" "+typ+def+notNull)
			}
		case "drop":
			out = append(out, "ALTER TABLE "+qTable+" DROP COLUMN "+qName)
		case "modify":
			typ, err := safeType(op.Type)
			if err != nil {
				return nil, err
			}
			nullability := ""
			if op.NotNull {
				nullability = " NOT NULL"
			}
			switch fam {
			case familyMySQL:
				out = append(out, "ALTER TABLE "+qTable+" MODIFY COLUMN "+qName+" "+typ+nullability)
			case familyClickHouse:
				out = append(out, "ALTER TABLE "+qTable+" MODIFY COLUMN "+qName+" "+typ)
			case familyPostgres:
				out = append(out, "ALTER TABLE "+qTable+" ALTER COLUMN "+qName+" TYPE "+typ)
				if op.NotNull {
					out = append(out, "ALTER TABLE "+qTable+" ALTER COLUMN "+qName+" SET NOT NULL")
				}
			case familySQLServer:
				out = append(out, "ALTER TABLE "+qTable+" ALTER COLUMN "+qName+" "+typ+nullability)
			case familyOracle:
				out = append(out, "ALTER TABLE "+qTable+" MODIFY ("+qName+" "+typ+nullability+")")
			}
		case "rename":
			newName := strings.TrimSpace(op.NewName)
			if newName == "" {
				return nil, fmt.Errorf("新列名不能为空")
			}
			qNew := quoteIdent(driver, newName)
			if fam == familySQLServer {
				out = append(out, "EXEC sp_rename '"+strings.ReplaceAll(table, "'", "''")+"."+strings.ReplaceAll(name, "'", "''")+"', '"+strings.ReplaceAll(newName, "'", "''")+"', 'COLUMN'")
			} else {
				out = append(out, "ALTER TABLE "+qTable+" RENAME COLUMN "+qName+" TO "+qNew)
			}
		case "addIndex":
			if fam == familyClickHouse {
				return nil, fmt.Errorf("ClickHouse 不支持二级索引，请使用主键或跳数索引")
			}
			cols := make([]string, 0, len(op.Columns))
			for _, col := range op.Columns {
				cols = append(cols, quoteIdent(driver, col))
			}
			if len(cols) == 0 {
				return nil, fmt.Errorf("索引至少需要一列")
			}
			unique := ""
			if op.Unique {
				unique = "UNIQUE "
			}
			idx := strings.TrimSpace(op.Name)
			if idx == "" {
				idx = "idx_" + table + "_" + strings.Join(op.Columns, "_")
			}
			out = append(out, "CREATE "+unique+"INDEX "+quoteIdent(driver, idx)+" ON "+qTable+" ("+strings.Join(cols, ", ")+")")
		case "dropIndex":
			if fam == familyMySQL {
				out = append(out, "DROP INDEX "+qName+" ON "+qTable)
			} else {
				out = append(out, "DROP INDEX "+qName)
			}
		default:
			return nil, fmt.Errorf("不支持的结构变更类型：%s", op.Kind)
		}
	}
	return out, nil
}

func buildSQLiteAlter(qTable string, ops []alterOp) ([]string, error) {
	var out []string
	for _, op := range ops {
		name := strings.TrimSpace(op.Name)
		if op.Kind != "addIndex" && name == "" {
			return nil, fmt.Errorf("列名不能为空")
		}
		qName := quoteIdent("sqlite", name)
		switch op.Kind {
		case "add":
			typ, err := safeType(op.Type)
			if err != nil {
				return nil, err
			}
			def := ""
			if strings.TrimSpace(op.Default) != "" {
				def = " DEFAULT " + strings.TrimSpace(op.Default)
			}
			out = append(out, "ALTER TABLE "+qTable+" ADD COLUMN "+qName+" "+typ+def)
		case "rename":
			newName := strings.TrimSpace(op.NewName)
			if newName == "" {
				return nil, fmt.Errorf("新列名不能为空")
			}
			out = append(out, "ALTER TABLE "+qTable+" RENAME COLUMN "+qName+" TO "+quoteIdent("sqlite", newName))
		case "drop":
			out = append(out, "ALTER TABLE "+qTable+" DROP COLUMN "+qName)
		case "addIndex":
			cols := make([]string, 0, len(op.Columns))
			for _, col := range op.Columns {
				cols = append(cols, quoteIdent("sqlite", col))
			}
			unique := ""
			if op.Unique {
				unique = "UNIQUE "
			}
			idx := strings.TrimSpace(op.Name)
			if idx == "" {
				idx = "idx_" + strings.Join(op.Columns, "_")
			}
			out = append(out, "CREATE "+unique+"INDEX "+quoteIdent("sqlite", idx)+" ON "+qTable+" ("+strings.Join(cols, ", ")+")")
		case "dropIndex":
			out = append(out, "DROP INDEX "+qName)
		default:
			return nil, fmt.Errorf("SQLite 不支持该结构变更（%s），请重建表", op.Kind)
		}
	}
	return out, nil
}

func (p *DB) loadTableColumns(ctx context.Context, conn Connection, db *sql.DB, database, schema, table string) ([]Column, error) {
	cols, err := p.loadTableColumnsRaw(ctx, conn, db, database, schema, table)
	if err != nil {
		return nil, err
	}
	switch familyOf(conn.Driver) {
	case familyPostgres, familyOracle, familySQLServer:
		p.markPrimaryKeys(ctx, conn, db, database, schema, table, cols)
	}
	return cols, nil
}

// markPrimaryKeys 补齐主键标记，行编辑需要准确定位记录。
func (p *DB) markPrimaryKeys(ctx context.Context, conn Connection, db *sql.DB, database, schema, table string, cols []Column) {
	var keys []string
	switch familyOf(conn.Driver) {
	case familyPostgres:
		if schema == "" {
			schema = "public"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT a.attname FROM pg_index i
			 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			 JOIN pg_class c ON c.oid = i.indrelid
			 JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE i.indisprimary AND n.nspname = $1 AND c.relname = $2`, schema, table)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil {
					keys = append(keys, name)
				}
			}
		}
	case familyOracle:
		owner := oracleOwner(conn, database, schema)
		rows, err := db.QueryContext(ctx,
			"SELECT c.column_name FROM all_constraints k"+
				" JOIN all_cons_columns c ON k.owner = c.owner AND k.constraint_name = c.constraint_name"+
				" WHERE k.constraint_type = 'P' AND k.owner = "+sqlLiteral(owner)+
				" AND k.table_name = "+sqlLiteral(table)+" ORDER BY c.position")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil {
					keys = append(keys, name)
				}
			}
		}
	case familySQLServer:
		if schema == "" {
			schema = "dbo"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT c.name FROM sys.indexes i
			 JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
			 JOIN sys.columns c ON ic.object_id = c.object_id AND ic.column_id = c.column_id
			 WHERE i.is_primary_key = 1 AND i.object_id = OBJECT_ID(@p1) ORDER BY ic.key_ordinal`, schema+"."+table)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil {
					keys = append(keys, name)
				}
			}
		}
	}
	if len(keys) == 0 {
		return
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	for i := range cols {
		if set[cols[i].Name] {
			cols[i].Key = "PRI"
		}
	}
}

func (p *DB) loadTableColumnsRaw(ctx context.Context, conn Connection, db *sql.DB, database, schema, table string) ([]Column, error) {
	switch familyOf(conn.Driver) {
	case familySQLite:
		rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdent(conn.Driver, table)+")")
		if err != nil {
			return nil, err
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
		return out, nil
	case familyPostgres:
		if schema == "" {
			schema = "public"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
			 FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2
			 ORDER BY ordinal_position`, schema, table)
		if err != nil {
			return nil, err
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
		return out, nil
	case familyOracle:
		owner := oracleOwner(conn, database, schema)
		rows, err := db.QueryContext(ctx,
			"SELECT column_name, data_type, nullable, COALESCE(data_default, '') FROM all_tab_columns"+
				" WHERE owner = "+sqlLiteral(owner)+" AND table_name = "+sqlLiteral(table)+" ORDER BY column_id")
		if err != nil {
			return nil, err
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
		return out, nil
	case familySQLServer:
		if schema == "" {
			schema = "dbo"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT c.name, t.name, CASE WHEN c.is_nullable = 1 THEN 'YES' ELSE 'NO' END, COALESCE(d.definition, '')
			 FROM sys.columns c
			 JOIN sys.types t ON c.user_type_id = t.user_type_id
			 LEFT JOIN sys.default_constraints d ON c.default_object_id = d.object_id
			 WHERE c.object_id = OBJECT_ID(@p1) ORDER BY c.column_id`, schema+"."+table)
		if err != nil {
			return nil, err
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
		return out, nil
	case familyClickHouse:
		if database == "" {
			database = resolveDatabase(conn, database)
		}
		rows, err := db.QueryContext(ctx,
			`SELECT name, type, if(default_kind = '', 'NO', 'YES'),
			        if(is_in_primary_key, 'PRI', ''), default_expression, '', comment
			 FROM system.columns WHERE database = ? AND table = ? ORDER BY position`, database, table)
		if err != nil {
			return nil, err
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
		return out, nil
	default:
		if database == "" {
			database = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COALESCE(COLUMN_DEFAULT, ''), EXTRA, COALESCE(COLUMN_COMMENT, '')
			 FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			 ORDER BY ORDINAL_POSITION`, database, table)
		if err != nil {
			return nil, err
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
		return out, nil
	}
}

func (p *DB) loadIndexes(ctx context.Context, conn Connection, db *sql.DB, database, schema, table string) ([]Index, []ForeignKey) {
	var indexes []Index
	var fks []ForeignKey
	fam := familyOf(conn.Driver)

	appendIndex := func(name string, unique, primary bool, columns []string, def string) {
		indexes = append(indexes, Index{Name: name, Unique: unique, Primary: primary, Columns: columns, Definition: def})
	}

	switch fam {
	case familySQLite:
		rows, err := db.QueryContext(ctx, "PRAGMA index_list("+quoteIdent(conn.Driver, table)+")")
		if err == nil {
			type idx struct {
				name    string
				unique  int
				origin  string
				partial int
			}
			var list []idx
			for rows.Next() {
				var (
					seq, unique, partial int
					name, origin         string
				)
				if rows.Scan(&seq, &name, &unique, &origin, &partial) != nil {
					continue
				}
				list = append(list, idx{name: name, unique: unique, origin: origin, partial: partial})
			}
			rows.Close()
			for _, it := range list {
				cols := sqliteIndexColumns(ctx, db, it.name)
				appendIndex(it.name, it.unique != 0, it.origin == "pk", cols, "")
			}
		}
		fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_list("+quoteIdent(conn.Driver, table)+")")
		if err == nil {
			type fk struct {
				id       int
				refTable string
				columns  []string
				refCols  []string
			}
			order := []int{}
			byID := map[int]*fk{}
			for fkRows.Next() {
				var (
					id, seq                      int
					refTable, from, to, onUpdate string
					onDelete, match              string
				)
				if fkRows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match) != nil {
					continue
				}
				item, ok := byID[id]
				if !ok {
					item = &fk{id: id, refTable: refTable}
					byID[id] = item
					order = append(order, id)
				}
				item.columns = append(item.columns, from)
				item.refCols = append(item.refCols, to)
			}
			fkRows.Close()
			for _, id := range order {
				item := byID[id]
				fks = append(fks, ForeignKey{Name: "fk_" + item.refTable + "_" + strconv.Itoa(id), Columns: item.columns, RefTable: item.refTable, RefColumns: item.refCols})
			}
		}

	case familyMySQL:
		if database == "" {
			database = conn.Database
		}
		rows, err := db.QueryContext(ctx,
			`SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS
			 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX`, database, table)
		if err == nil {
			type idx struct {
				unique bool
				cols   []string
			}
			order := []string{}
			byName := map[string]*idx{}
			for rows.Next() {
				var name, col string
				var nonUnique int
				if rows.Scan(&name, &nonUnique, &col) != nil {
					continue
				}
				item, ok := byName[name]
				if !ok {
					item = &idx{unique: nonUnique == 0}
					byName[name] = item
					order = append(order, name)
				}
				item.cols = append(item.cols, col)
			}
			rows.Close()
			for _, name := range order {
				item := byName[name]
				appendIndex(name, item.unique, strings.EqualFold(name, "PRIMARY"), item.cols, "")
			}
		}
		fkRows, err := db.QueryContext(ctx,
			`SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
			 FROM information_schema.KEY_COLUMN_USAGE
			 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL
			 ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`, database, table)
		if err == nil {
			type fk struct {
				refTable      string
				cols, refCols []string
			}
			order := []string{}
			byName := map[string]*fk{}
			for fkRows.Next() {
				var name, col, refTable, refCol string
				if fkRows.Scan(&name, &col, &refTable, &refCol) != nil {
					continue
				}
				item, ok := byName[name]
				if !ok {
					item = &fk{refTable: refTable}
					byName[name] = item
					order = append(order, name)
				}
				item.cols = append(item.cols, col)
				item.refCols = append(item.refCols, refCol)
			}
			fkRows.Close()
			for _, name := range order {
				item := byName[name]
				fks = append(fks, ForeignKey{Name: name, Columns: item.cols, RefTable: item.refTable, RefColumns: item.refCols})
			}
		}

	case familyPostgres:
		if schema == "" {
			schema = "public"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2 ORDER BY indexname`, schema, table)
		if err == nil {
			for rows.Next() {
				var name, def string
				if rows.Scan(&name, &def) != nil {
					continue
				}
				unique := strings.Contains(def, " UNIQUE INDEX ")
				primary := strings.Contains(def, "PRIMARY KEY") || strings.HasSuffix(name, "_pkey")
				appendIndex(name, unique, primary, parseIndexColumns(def), def)
			}
			rows.Close()
		}
		fkRows, err := db.QueryContext(ctx,
			`SELECT tc.constraint_name, kcu.column_name, ccu.table_name, ccu.column_name
			 FROM information_schema.table_constraints tc
			 JOIN information_schema.key_column_usage kcu
			   ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			 JOIN information_schema.constraint_column_usage ccu
			   ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
			 WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = $1 AND tc.table_name = $2
			 ORDER BY tc.constraint_name, kcu.ordinal_position`, schema, table)
		if err == nil {
			type fk struct {
				refTable      string
				cols, refCols []string
			}
			order := []string{}
			byName := map[string]*fk{}
			for fkRows.Next() {
				var name, col, refTable, refCol string
				if fkRows.Scan(&name, &col, &refTable, &refCol) != nil {
					continue
				}
				item, ok := byName[name]
				if !ok {
					item = &fk{refTable: refTable}
					byName[name] = item
					order = append(order, name)
				}
				item.cols = append(item.cols, col)
				item.refCols = append(item.refCols, refCol)
			}
			fkRows.Close()
			for _, name := range order {
				item := byName[name]
				fks = append(fks, ForeignKey{Name: name, Columns: item.cols, RefTable: item.refTable, RefColumns: item.refCols})
			}
		}

	case familyOracle:
		owner := oracleOwner(conn, database, schema)
		rows, err := db.QueryContext(ctx,
			"SELECT i.index_name, i.uniqueness, c.column_name FROM all_indexes i"+
				" JOIN all_ind_columns c ON i.owner = c.index_owner AND i.index_name = c.index_name"+
				" WHERE i.table_owner = "+sqlLiteral(owner)+" AND i.table_name = "+sqlLiteral(table)+
				" ORDER BY i.index_name, c.column_position")
		if err == nil {
			type idx struct {
				unique bool
				cols   []string
			}
			order := []string{}
			byName := map[string]*idx{}
			for rows.Next() {
				var name, uniqueness, col string
				if rows.Scan(&name, &uniqueness, &col) != nil {
					continue
				}
				item, ok := byName[name]
				if !ok {
					item = &idx{unique: strings.EqualFold(uniqueness, "UNIQUE")}
					byName[name] = item
					order = append(order, name)
				}
				item.cols = append(item.cols, col)
			}
			rows.Close()
			for _, name := range order {
				item := byName[name]
				appendIndex(name, item.unique, strings.HasPrefix(name, "PK_") || strings.HasPrefix(name, "SYS_P"), item.cols, "")
			}
		}

	case familySQLServer:
		if schema == "" {
			schema = "dbo"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT i.name, i.is_unique, i.is_primary_key, c.name
			 FROM sys.indexes i
			 JOIN sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
			 JOIN sys.columns c ON ic.object_id = c.object_id AND ic.column_id = c.column_id
			 WHERE i.object_id = OBJECT_ID(@p1) ORDER BY i.index_id, ic.key_ordinal`, schema+"."+table)
		if err == nil {
			type idx struct {
				unique, primary bool
				cols            []string
			}
			order := []string{}
			byName := map[string]*idx{}
			for rows.Next() {
				var name, col string
				var unique, primary bool
				if rows.Scan(&name, &unique, &primary, &col) != nil {
					continue
				}
				item, ok := byName[name]
				if !ok {
					item = &idx{unique: unique, primary: primary}
					byName[name] = item
					order = append(order, name)
				}
				item.cols = append(item.cols, col)
			}
			rows.Close()
			for _, name := range order {
				item := byName[name]
				appendIndex(name, item.unique, item.primary, item.cols, "")
			}
		}
		fkRows, err := db.QueryContext(ctx,
			`SELECT fk.name, pc.name, OBJECT_NAME(fk.referenced_object_id), rc.name
			 FROM sys.foreign_keys fk
			 JOIN sys.foreign_key_columns fkc ON fk.object_id = fkc.constraint_object_id
			 JOIN sys.columns pc ON fkc.parent_object_id = pc.object_id AND fkc.parent_column_id = pc.column_id
			 JOIN sys.columns rc ON fkc.referenced_object_id = rc.object_id AND fkc.referenced_column_id = rc.column_id
			 WHERE fk.parent_object_id = OBJECT_ID(@p1) ORDER BY fk.name, fkc.constraint_column_id`, schema+"."+table)
		if err == nil {
			type fk struct {
				refTable      string
				cols, refCols []string
			}
			order := []string{}
			byName := map[string]*fk{}
			for fkRows.Next() {
				var name, col, refTable, refCol string
				if fkRows.Scan(&name, &col, &refTable, &refCol) != nil {
					continue
				}
				item, ok := byName[name]
				if !ok {
					item = &fk{refTable: refTable}
					byName[name] = item
					order = append(order, name)
				}
				item.cols = append(item.cols, col)
				item.refCols = append(item.refCols, refCol)
			}
			fkRows.Close()
			for _, name := range order {
				item := byName[name]
				fks = append(fks, ForeignKey{Name: name, Columns: item.cols, RefTable: item.refTable, RefColumns: item.refCols})
			}
		}

	case familyClickHouse:
		if database == "" {
			database = resolveDatabase(conn, database)
		}
		rows, err := db.QueryContext(ctx,
			"SELECT name FROM system.columns WHERE database = ? AND table = ? AND is_in_primary_key ORDER BY position", database, table)
		if err == nil {
			var cols []string
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil {
					cols = append(cols, name)
				}
			}
			rows.Close()
			if len(cols) > 0 {
				appendIndex("PRIMARY", true, true, cols, "")
			}
		}
	}
	return indexes, fks
}

func sqliteIndexColumns(ctx context.Context, db *sql.DB, name string) []string {
	rows, err := db.QueryContext(ctx, "PRAGMA index_info("+quoteIdent("sqlite", name)+")")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var col string
		if rows.Scan(&seqno, &cid, &col) == nil {
			cols = append(cols, col)
		}
	}
	return cols
}

func parseIndexColumns(def string) []string {
	open := strings.Index(def, "(")
	closeIdx := strings.LastIndex(def, ")")
	if open < 0 || closeIdx <= open {
		return nil
	}
	raw := def[open+1 : closeIdx]
	parts := strings.Split(raw, ",")
	cols := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(part, " DESC")
		part = strings.TrimSuffix(part, " ASC")
		part = strings.Trim(part, `"`)
		if part != "" {
			cols = append(cols, part)
		}
	}
	return cols
}
