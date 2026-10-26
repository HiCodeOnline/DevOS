package dbapp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/beltran/gohive"
)

// hive 族通过 gohive 的 HiveServer2 协议接入 Hive / Spark Thrift / Kyuubi / Impala。
// 这里实现一个最小的 database/sql 驱动，复用通用的表浏览、导出与查询流程。
// DSN 形如：hive://user:pass@host:10000/default?auth=NOSASL&transport=binary
func init() {
	sql.Register("hive", hiveDriver{})
}

type hiveDriver struct{}

func (hiveDriver) Open(dsn string) (driver.Conn, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("Hive DSN 解析失败: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("Hive 主机地址不能为空")
	}
	port := 10000
	if p := u.Port(); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}
	params := u.Query()
	cfg := gohive.NewConnectConfiguration()
	cfg.Username = u.User.Username()
	if pass, ok := u.User.Password(); ok {
		cfg.Password = pass
	}
	cfg.Database = strings.TrimPrefix(u.Path, "/")
	if cfg.Database == "" {
		cfg.Database = "default"
	}
	if v := params.Get("fetchSize"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.FetchSize = n
		}
	}
	if t := params.Get("transport"); t != "" {
		cfg.TransportMode = t
	}
	if p := params.Get("httpPath"); p != "" {
		cfg.HTTPPath = p
	}
	if ns := params.Get("zookeeperNamespace"); ns != "" {
		cfg.ZookeeperNamespace = ns
	}
	auth := strings.ToUpper(params.Get("auth"))
	if auth == "" {
		if cfg.Username != "" {
			auth = "LDAP"
		} else {
			auth = "NOSASL"
		}
	}
	conn, err := gohive.Connect(host, port, auth, cfg)
	if err != nil {
		return nil, err
	}
	return &hiveConn{conn: conn, ctx: context.Background()}, nil
}

type hiveConn struct {
	conn *gohive.Connection
	ctx  context.Context
}

func (c *hiveConn) Prepare(query string) (driver.Stmt, error) {
	return &hiveStmt{conn: c, query: query}, nil
}

func (c *hiveConn) Close() error {
	c.conn.Close()
	return nil
}

// Begin 不支持事务，返回未实现错误以提示用户。
func (c *hiveConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("Hive 协议不支持事务")
}

func (c *hiveConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		return nil, fmt.Errorf("Hive 查询不支持参数占位符，请直接拼接字面量")
	}
	cursor := c.conn.Cursor()
	cursor.Execute(ctx, query, false)
	if err := cursor.Error(); err != nil {
		cursor.Close()
		return nil, err
	}
	desc := cursor.Description()
	if err := cursor.Error(); err != nil {
		cursor.Close()
		return nil, err
	}
	cols := make([]string, 0, len(desc))
	for _, d := range desc {
		if len(d) > 0 {
			cols = append(cols, d[0])
		}
	}
	return &hiveRows{cursor: cursor, cols: cols}, nil
}

func (c *hiveConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) > 0 {
		return nil, fmt.Errorf("Hive 语句不支持参数占位符，请直接拼接字面量")
	}
	cursor := c.conn.Cursor()
	defer cursor.Close()
	cursor.Exec(ctx, query)
	if err := cursor.Error(); err != nil {
		return nil, err
	}
	return driver.ResultNoRows, nil
}

type hiveStmt struct {
	conn  *hiveConn
	query string
}

func (s *hiveStmt) Close() error  { return nil }
func (s *hiveStmt) NumInput() int { return -1 }

func (s *hiveStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.conn.ExecContext(s.conn.ctx, s.query, namedValues(args))
}

func (s *hiveStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.conn.QueryContext(s.conn.ctx, s.query, namedValues(args))
}

func namedValues(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

type hiveRows struct {
	cursor *gohive.Cursor
	cols   []string
	done   bool
}

func (r *hiveRows) Columns() []string { return r.cols }

func (r *hiveRows) Close() error {
	r.cursor.Close()
	return nil
}

func (r *hiveRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	if !r.cursor.HasMore(context.Background()) {
		if err := r.cursor.Error(); err != nil {
			r.done = true
			return err
		}
		r.done = true
		return io.EOF
	}
	m := r.cursor.RowMap(context.Background())
	if err := r.cursor.Error(); err != nil {
		r.done = true
		return err
	}
	if m == nil {
		r.done = true
		return io.EOF
	}
	for i, name := range r.cols {
		dest[i] = normalizeHiveValue(m[name])
	}
	return nil
}

// normalizeHiveValue 把 gohive 的原始值转换为 database/sql 可接受的类型。
func normalizeHiveValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case int8, int16, int32, int64, float32, float64, bool, string:
		return v
	default:
		return fmt.Sprint(t)
	}
}
