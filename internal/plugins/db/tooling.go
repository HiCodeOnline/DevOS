package dbapp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

/* ---------------- SQL 文件批量执行 ---------------- */

// splitSQLStatements 按分号切分 SQL 脚本，忽略字符串与注释中的分号。
func splitSQLStatements(input string) []string {
	var out []string
	var cur strings.Builder
	i, n := 0, len(input)
	for i < n {
		ch := input[i]
		switch {
		case ch == '\'' || ch == '"' || ch == '`':
			end := quotedEnd(input, i, ch)
			cur.WriteString(input[i:end])
			i = end
		case ch == '[' && strings.Contains(input[i:], "]"):
			// SQL Server 的方括号标识符。
			end := strings.IndexByte(input[i:], ']')
			cur.WriteString(input[i : i+end+1])
			i += end + 1
		case ch == '$' && i+1 < n && (input[i+1] == '$' || isTagDollar(input[i+1])):
			tagEnd := strings.IndexByte(input[i+1:], '$')
			if tagEnd < 0 {
				cur.WriteByte(ch)
				i++
				break
			}
			tag := input[i : i+tagEnd+2]
			if pos := strings.Index(input[i+len(tag):], tag); pos >= 0 {
				end := i + len(tag) + pos + len(tag)
				cur.WriteString(input[i:end])
				i = end
			} else {
				cur.WriteByte(ch)
				i++
			}
		case ch == '-' && i+1 < n && input[i+1] == '-':
			j := strings.IndexByte(input[i:], '\n')
			if j < 0 {
				i = n
			} else {
				cur.WriteString(input[i : i+j+1])
				i += j + 1
			}
		case ch == '#':
			j := strings.IndexByte(input[i:], '\n')
			if j < 0 {
				i = n
			} else {
				cur.WriteString(input[i : i+j+1])
				i += j + 1
			}
		case ch == '/' && i+1 < n && input[i+1] == '*':
			j := strings.Index(input[i+2:], "*/")
			if j < 0 {
				i = n
			} else {
				cur.WriteString(input[i : i+j+4])
				i += j + 4
			}
		case ch == ';':
			if stmt := strings.TrimSpace(cur.String()); stmt != "" {
				out = append(out, stmt)
			}
			cur.Reset()
			i++
		default:
			cur.WriteByte(ch)
			i++
		}
	}
	if stmt := strings.TrimSpace(cur.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func isTagDollar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// quotedEnd 返回引号字符串的结束位置（不包含引号的转义处理差异）。
func quotedEnd(input string, start int, quote byte) int {
	i := start + 1
	for i < len(input) {
		if input[i] == '\\' && quote != '`' {
			i += 2
			continue
		}
		if input[i] == quote {
			if i+1 < len(input) && input[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return len(input)
}

func (p *DB) handleSQLFile(c *fiber.Ctx) error {
	var in struct {
		ID       string `json:"id"`
		Database string `json:"database"`
		SQL      string `json:"sql"`
		Continue bool   `json:"continueOnError"`
		MaxRows  int    `json:"maxRows"`
		Confirm  bool   `json:"confirm"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if !typeOf(conn.Driver).SQL {
		return jsonErr(c, fiber.StatusBadRequest, "该连接类型不支持 SQL 脚本执行")
	}
	statements := splitSQLStatements(in.SQL)
	if len(statements) == 0 {
		return jsonErr(c, fiber.StatusBadRequest, "脚本中没有可执行的语句")
	}
	if len(statements) > 500 {
		return jsonErr(c, fiber.StatusBadRequest, "单次最多执行 500 条语句")
	}
	// 与单条执行一致：脚本中若存在缺少过滤条件的语句，同样需要用户确认。
	if !in.Confirm {
		for _, stmt := range statements {
			if reason := sqlGuardReason(stmt); reason != "" {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{
					"error":        reason,
					"needsConfirm": true,
				})
			}
		}
	}
	db, err := p.pool(conn, in.Database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	maxRows := in.MaxRows
	if maxRows <= 0 {
		maxRows = 500
	}
	if maxRows > 5000 {
		maxRows = 5000
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	results := make([]fiber.Map, 0, len(statements))
	for idx, stmt := range statements {
		start := time.Now()
		item := fiber.Map{"index": idx + 1, "statement": stmt}
		if conn.ReadOnly && isWriteSQL(stmt) {
			item["error"] = "该连接为只读模式，已拒绝执行写操作"
			results = append(results, item)
			if !in.Continue {
				break
			}
			continue
		}
		if isWriteSQL(stmt) {
			res, execErr := db.ExecContext(ctx, stmt)
			if execErr != nil {
				item["error"] = execErr.Error()
				results = append(results, item)
				if !in.Continue {
					break
				}
				continue
			}
			affected, _ := res.RowsAffected()
			item["kind"] = "exec"
			item["affected"] = affected
			item["durationMs"] = time.Since(start).Milliseconds()
			results = append(results, item)
			continue
		}
		rows, queryErr := db.QueryContext(ctx, stmt)
		if queryErr != nil {
			item["error"] = queryErr.Error()
			results = append(results, item)
			if !in.Continue {
				break
			}
			continue
		}
		cols, data, truncated, scanErr := scanAll(rows, maxRows)
		rows.Close()
		if scanErr != nil {
			item["error"] = scanErr.Error()
			results = append(results, item)
			if !in.Continue {
				break
			}
			continue
		}
		item["kind"] = "rows"
		item["columns"] = cols
		item["rows"] = data
		item["rowCount"] = len(data)
		item["truncated"] = truncated
		item["durationMs"] = time.Since(start).Milliseconds()
		results = append(results, item)
	}
	failed := 0
	for _, r := range results {
		if _, ok := r["error"]; ok {
			failed++
		}
	}
	return c.JSON(fiber.Map{"total": len(statements), "executed": len(results), "failed": failed, "results": results})
}

/* ---------------- 执行计划 ---------------- */

func (p *DB) handleExplain(c *fiber.Ctx) error {
	var in struct {
		ID       string `json:"id"`
		Database string `json:"database"`
		SQL      string `json:"sql"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	sqlText := strings.TrimSpace(in.SQL)
	if sqlText == "" {
		return jsonErr(c, fiber.StatusBadRequest, "请输入 SQL 语句")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if !typeOf(conn.Driver).SQL {
		return jsonErr(c, fiber.StatusBadRequest, "该连接类型不支持执行计划")
	}
	db, err := p.pool(conn, in.Database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()

	fam := familyOf(conn.Driver)
	var rows *sql.Rows

	switch fam {
	case familySQLServer:
		sconn, connErr := db.Conn(ctx)
		if connErr != nil {
			return jsonErr(c, fiber.StatusBadGateway, connErr.Error())
		}
		defer sconn.Close()
		if _, err := sconn.ExecContext(ctx, "SET SHOWPLAN_ALL ON"); err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		rows, err = sconn.QueryContext(ctx, sqlText)
		if err != nil {
			_, _ = sconn.ExecContext(ctx, "SET SHOWPLAN_ALL OFF")
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		defer func() { _, _ = sconn.ExecContext(context.Background(), "SET SHOWPLAN_ALL OFF") }()

	case familyOracle:
		if typeOf(conn.Driver).ID == "dameng" {
			rows, err = db.QueryContext(ctx, "EXPLAIN "+sqlText)
			break
		}
		tx, txErr := db.BeginTx(ctx, nil)
		if txErr != nil {
			return jsonErr(c, fiber.StatusBadGateway, txErr.Error())
		}
		if _, err := tx.ExecContext(ctx, "EXPLAIN PLAN FOR "+sqlText); err != nil {
			_ = tx.Rollback()
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		rows, err = db.QueryContext(ctx, "SELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())")
		if err == nil && rows != nil {
			defer rows.Close()
			cols, data, truncated, scanErr := scanAll(rows, 500)
			_ = tx.Rollback()
			if scanErr != nil {
				return jsonErr(c, fiber.StatusBadGateway, scanErr.Error())
			}
			return c.JSON(fiber.Map{"columns": cols, "rows": data, "rowCount": len(data), "truncated": truncated, "durationMs": time.Since(start).Milliseconds()})
		}
		_ = tx.Rollback()
		if err == nil {
			err = fmt.Errorf("未获取到执行计划")
		}

	case familySQLite:
		rows, err = db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sqlText)
	default:
		rows, err = db.QueryContext(ctx, "EXPLAIN "+sqlText)
	}
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()
	cols, data, truncated, err := scanAll(rows, 500)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{"columns": cols, "rows": data, "rowCount": len(data), "truncated": truncated, "durationMs": time.Since(start).Milliseconds()})
}

/* ---------------- SQL 导出 ---------------- */

// exportSQL 将整表导出为建表语句 + INSERT 语句。
func (p *DB) exportSQL(c *fiber.Ctx, conn Connection, database, schema, table string) error {
	db, err := p.pool(conn, database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cols, err := p.loadTableColumns(ctx, conn, db, database, schema, table)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	if len(cols) == 0 {
		return jsonErr(c, fiber.StatusBadGateway, "未读取到表结构")
	}

	qualified := qualify(conn.Driver, database, schema, table)
	var b strings.Builder
	b.WriteString("-- DevOS 数据库导出\n")
	b.WriteString("-- 表：" + table + "\n")
	b.WriteString("-- 时间：" + time.Now().Format("2006-01-02 15:04:05") + "\n\n")
	b.WriteString(createTableSQL(conn.Driver, qualified, cols))

	rows, err := db.QueryContext(ctx, "SELECT * FROM "+qualified)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()

	names, err := rows.Columns()
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteIdent(conn.Driver, n)
	}
	insertPrefix := "INSERT INTO " + qualified + " (" + strings.Join(quoted, ", ") + ") VALUES\n"
	batch := make([]string, 0, 50)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		b.WriteString(insertPrefix)
		b.WriteString(strings.Join(batch, ",\n"))
		b.WriteString(";\n")
		batch = batch[:0]
	}
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		record := make([]string, len(vals))
		for i, v := range vals {
			record[i] = sqlValue(normalizeValue(v))
		}
		batch = append(batch, "("+strings.Join(record, ", ")+")")
		if len(batch) >= 50 {
			flush()
		}
	}
	flush()

	c.Set("Content-Type", "application/sql; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(table)+`.sql"`)
	return c.SendString(b.String())
}

func createTableSQL(driver, qualified string, cols []Column) string {
	fam := familyOf(driver)
	var pk []string
	lines := make([]string, 0, len(cols)+1)
	for _, col := range cols {
		line := "  " + quoteIdent(driver, col.Name) + " " + col.Type
		if strings.EqualFold(col.Nullable, "NO") {
			line += " NOT NULL"
		}
		if d := strings.TrimSpace(col.Default); d != "" {
			line += " DEFAULT " + d
		}
		lines = append(lines, line)
		if strings.EqualFold(col.Key, "PRI") {
			pk = append(pk, quoteIdent(driver, col.Name))
		}
	}
	if len(pk) > 0 {
		lines = append(lines, "  PRIMARY KEY ("+strings.Join(pk, ", ")+")")
	}
	sqlText := "CREATE TABLE " + qualified + " (\n" + strings.Join(lines, ",\n") + "\n)"
	if fam == familyClickHouse {
		order := "tuple()"
		if len(pk) > 0 {
			order = "(" + strings.Join(pk, ", ") + ")"
		}
		sqlText += " ENGINE = MergeTree ORDER BY " + order
	}
	return sqlText + ";\n\n"
}

func sqlValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "1"
		}
		return "0"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	case []byte:
		return "'" + strings.ReplaceAll(string(t), "'", "''") + "'"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(t)
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(t), "'", "''") + "'"
	}
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	out := replacer.Replace(name)
	if out == "" {
		out = "export"
	}
	return out
}
