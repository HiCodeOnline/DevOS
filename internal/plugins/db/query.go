package dbapp

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
)

// scanAll 读取结果集，max <= 0 表示不限制。
func scanAll(rows *sql.Rows, max int) ([]string, [][]any, bool, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	out := make([][]any, 0, 64)
	truncated := false
	for rows.Next() {
		if max > 0 && len(out) >= max {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return cols, out, truncated, err
		}
		for i, v := range vals {
			vals[i] = normalizeValue(v)
		}
		out = append(out, vals)
	}
	return cols, out, truncated, rows.Err()
}

// normalizeValue 将数据库原始值转换为可 JSON 序列化的值。
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(t) {
			return string(t)
		}
		return base64.StdEncoding.EncodeToString(t)
	case time.Time:
		if t.Nanosecond() == 0 {
			return t.Format("2006-01-02 15:04:05")
		}
		return t.Format("2006-01-02 15:04:05.999999")
	default:
		return v
	}
}

// isWriteSQL 粗略判断语句是否会写入数据。
func isWriteSQL(q string) bool {
	up := strings.ToUpper(strings.TrimLeft(q, " \t\r\n("))
	for _, kw := range []string{
		"INSERT", "UPDATE", "DELETE", "REPLACE", "CREATE", "ALTER", "DROP",
		"TRUNCATE", "GRANT", "REVOKE", "MERGE", "VACUUM", "ATTACH", "DETACH",
		"BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT",
	} {
		if strings.HasPrefix(up, kw) {
			return true
		}
	}
	return false
}

func (p *DB) handleQuery(c *fiber.Ctx) error {
	var in struct {
		ID       string `json:"id"`
		Database string `json:"database"`
		SQL      string `json:"sql"`
		MaxRows  int    `json:"maxRows"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	if strings.TrimSpace(in.SQL) == "" {
		return jsonErr(c, fiber.StatusBadRequest, "请输入 SQL 语句")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	switch familyOf(conn.Driver) {
	case familyRedis:
		return jsonErr(c, fiber.StatusBadRequest, "Redis 连接请在「命令」控制台执行命令")
	case familyMongo:
		return jsonErr(c, fiber.StatusBadRequest, "MongoDB 连接请在「命令」控制台执行 JSON 命令")
	case familyHTTP:
		if conn.ReadOnly && httpRequestIsWrite(in.SQL) {
			return jsonErr(c, fiber.StatusForbidden, "该连接为只读模式，已拒绝执行写操作")
		}
		start := time.Now()
		res, err := p.httpQuery(conn, in.Database, in.SQL, in.MaxRows)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		payload := fiber.Map{
			"kind":       res.Kind,
			"durationMs": time.Since(start).Milliseconds(),
		}
		if res.Kind == "rows" {
			payload["columns"] = res.Columns
			payload["rows"] = res.Rows
			payload["rowCount"] = len(res.Rows)
			payload["truncated"] = res.Truncated
		} else {
			payload["affected"] = res.Affected
		}
		return c.JSON(payload)
	case familyCassandra, familyZooKeeper, familyMQTT, familyNebula:
		if conn.ReadOnly {
			return jsonErr(c, fiber.StatusForbidden, "该连接为只读模式，已拒绝执行写操作")
		}
		start := time.Now()
		res, err := p.nativeExec(conn, in.Database, in.SQL, in.MaxRows)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		payload := fiber.Map{
			"kind":       res.Kind,
			"durationMs": time.Since(start).Milliseconds(),
		}
		if res.Kind == "rows" {
			payload["columns"] = res.Columns
			payload["rows"] = res.Rows
			payload["rowCount"] = len(res.Rows)
			payload["truncated"] = res.Truncated
		} else {
			payload["affected"] = res.Affected
		}
		return c.JSON(payload)
	}
	if conn.ReadOnly && isWriteSQL(in.SQL) {
		return jsonErr(c, fiber.StatusForbidden, "该连接为只读模式，已拒绝执行写操作")
	}
	db, err := p.pool(conn, in.Database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	maxRows := in.MaxRows
	if maxRows <= 0 {
		maxRows = 2000
	}
	if maxRows > 20000 {
		maxRows = 20000
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	if isWriteSQL(in.SQL) {
		res, err := db.ExecContext(ctx, in.SQL)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		affected, _ := res.RowsAffected()
		lastID, _ := res.LastInsertId()
		return c.JSON(fiber.Map{
			"kind":       "exec",
			"affected":   affected,
			"lastId":     lastID,
			"durationMs": time.Since(start).Milliseconds(),
		})
	}

	rows, err := db.QueryContext(ctx, in.SQL)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()

	cols, data, truncated, err := scanAll(rows, maxRows)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{
		"kind":       "rows",
		"columns":    cols,
		"rows":       data,
		"rowCount":   len(data),
		"truncated":  truncated,
		"durationMs": time.Since(start).Milliseconds(),
	})
}

// tableQuery 构造表数据查询语句，分页语法按协议族适配。
func tableQuery(driver, database, schema, table, sort, dir string, page, pageSize int) string {
	q := "SELECT * FROM " + qualify(driver, database, schema, table)
	order := ""
	if sort != "" {
		d := strings.ToUpper(dir)
		if d != "DESC" {
			d = "ASC"
		}
		order = " ORDER BY " + quoteIdent(driver, sort) + " " + d
	}
	if pageSize <= 0 {
		return q + order
	}
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	n := strconv.Itoa(pageSize)
	m := strconv.Itoa(offset)

	switch familyOf(driver) {
	case familySQLServer:
		if order == "" {
			// OFFSET/FETCH 语法要求存在 ORDER BY。
			order = " ORDER BY (SELECT NULL)"
		}
		return q + order + " OFFSET " + m + " ROWS FETCH NEXT " + n + " ROWS ONLY"
	case familyOracle:
		if typeOf(driver).ID == "dameng" {
			return q + order + " LIMIT " + n + " OFFSET " + m
		}
		return q + order + " OFFSET " + m + " ROWS FETCH NEXT " + n + " ROWS ONLY"
	case familyFirebird:
		return q + order + firebirdPage(page, pageSize, offset)
	default:
		return q + order + " LIMIT " + n + " OFFSET " + m
	}
}

func (p *DB) handleTableData(c *fiber.Ctx) error {
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
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.Query("pageSize", "100"))
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 100
	}
	sortCol := c.Query("sort")
	dir := c.Query("dir")
	switch familyOf(conn.Driver) {
	case familyHTTP, familyCassandra, familyZooKeeper, familyMQTT, familyNebula:
		return jsonErr(c, fiber.StatusBadRequest, "该连接类型请在「查询」面板执行语句")
	}
	db, err := p.pool(conn, database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	qualified := qualify(conn.Driver, database, schema, table)
	var total int64 = -1
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qualified).Scan(&total); err != nil {
		total = -1
	}

	start := time.Now()
	rows, err := db.QueryContext(ctx, tableQuery(conn.Driver, database, schema, table, sortCol, dir, page, pageSize))
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()

	cols, data, truncated, err := scanAll(rows, pageSize)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	return c.JSON(fiber.Map{
		"columns":    cols,
		"rows":       data,
		"rowCount":   len(data),
		"truncated":  truncated,
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"durationMs": time.Since(start).Milliseconds(),
	})
}

func (p *DB) handleExport(c *fiber.Ctx) error {
	var in struct {
		ID       string `json:"id"`
		Database string `json:"database"`
		Schema   string `json:"schema"`
		Table    string `json:"table"`
		SQL      string `json:"sql"`
		Sort     string `json:"sort"`
		Dir      string `json:"dir"`
		Format   string `json:"format"`
	}
	if err := c.BodyParser(&in); err != nil {
		return jsonErr(c, fiber.StatusBadRequest, "参数解析失败")
	}
	conn, err := p.conn(in.ID)
	if err != nil {
		return jsonErr(c, fiber.StatusBadRequest, err.Error())
	}
	if strings.EqualFold(in.Format, "sql") {
		table := strings.TrimSpace(in.Table)
		if table == "" {
			return jsonErr(c, fiber.StatusBadRequest, "SQL 导出仅支持整表导出，请提供表名")
		}
		if !typeOf(conn.Driver).SQL {
			return jsonErr(c, fiber.StatusBadRequest, "该连接类型不支持 SQL 导出")
		}
		return p.exportSQL(c, conn, in.Database, in.Schema, table)
	}
	q := strings.TrimSpace(in.SQL)
	if q == "" {
		if in.Table == "" {
			return jsonErr(c, fiber.StatusBadRequest, "请提供表名或 SQL")
		}
		q = tableQuery(conn.Driver, in.Database, in.Schema, in.Table, in.Sort, in.Dir, 1, 0)
	}

	if familyOf(conn.Driver) == familyHTTP {
		res, err := p.httpQuery(conn, in.Database, q, 20000)
		if err != nil {
			return jsonErr(c, fiber.StatusBadGateway, err.Error())
		}
		return writeCSV(c, res.Columns, res.Rows)
	}

	db, err := p.pool(conn, in.Database)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return jsonErr(c, fiber.StatusBadGateway, err.Error())
	}

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="devos-export.csv"`)

	w := csv.NewWriter(c.Response().BodyWriter())
	_, _ = c.Response().BodyWriter().Write([]byte{0xEF, 0xBB, 0xBF})
	_ = w.Write(cols)

	writeErr := error(nil)
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			writeErr = err
			break
		}
		record := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				record[i] = ""
			} else {
				record[i] = fmt.Sprint(normalizeValue(v))
			}
		}
		if err := w.Write(record); err != nil {
			writeErr = err
			break
		}
	}
	w.Flush()
	if writeErr != nil {
		p.log.Warn("导出 CSV 中断", "err", writeErr)
	}
	return nil
}

// writeCSV 将结果集以 UTF-8 BOM 的 CSV 形式直接写回响应。
func writeCSV(c *fiber.Ctx, cols []string, rows [][]any) error {
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="devos-export.csv"`)
	w := csv.NewWriter(c.Response().BodyWriter())
	_, _ = c.Response().BodyWriter().Write([]byte{0xEF, 0xBB, 0xBF})
	_ = w.Write(cols)
	for _, row := range rows {
		record := make([]string, len(cols))
		for i := range cols {
			if i < len(row) && row[i] != nil {
				record[i] = fmt.Sprint(row[i])
			}
		}
		if err := w.Write(record); err != nil {
			break
		}
	}
	w.Flush()
	return nil
}
