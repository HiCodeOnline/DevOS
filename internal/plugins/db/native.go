package dbapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.mqtt.golang"
	"github.com/go-zookeeper/zk"
	"github.com/gocql/gocql"
	nebula "github.com/vesoft-inc/nebula-go/v3"
)

// 本文件实现需要原生客户端的类型：Cassandra（CQL）、ZooKeeper、MQTT、NebulaGraph（nGQL）。
// 客户端按连接缓存，连接被删除时统一关闭，避免资源泄漏。

// nativePing 探测原生客户端连接并返回版本信息。
func (p *DB) nativePing(c Connection, database string) (int64, string, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	switch familyOf(c.Driver) {
	case familyCassandra:
		session, err := p.cassandraSession(c)
		if err != nil {
			return 0, "", err
		}
		var version string
		if err := session.Query("SELECT release_version FROM system.local").WithContext(ctx).Scan(&version); err != nil {
			return 0, "", err
		}
		return time.Since(start).Milliseconds(), "Cassandra " + version, nil

	case familyZooKeeper:
		conn, err := p.zkConn(c)
		if err != nil {
			return 0, "", err
		}
		if _, _, err := conn.Get("/"); err != nil {
			return 0, "", err
		}
		return time.Since(start).Milliseconds(), "ZooKeeper", nil

	case familyMQTT:
		client, err := newMQTTClient(c)
		if err != nil {
			return 0, "", err
		}
		defer client.Disconnect(250)
		return time.Since(start).Milliseconds(), "MQTT broker", nil

	case familyNebula:
		sess, release, err := p.nebulaSession(c)
		if err != nil {
			return 0, "", err
		}
		defer release()
		if err := sess.Ping(); err != nil {
			return 0, "", err
		}
		return time.Since(start).Milliseconds(), "NebulaGraph", nil
	}
	return 0, "", fmt.Errorf("该类型不支持原生探测")
}

// nativeDatabases 列出命名空间。
func (p *DB) nativeDatabases(c Connection) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch familyOf(c.Driver) {
	case familyCassandra:
		session, err := p.cassandraSession(c)
		if err != nil {
			return nil, err
		}
		iter := session.Query("SELECT keyspace_name FROM system_schema.keyspaces").WithContext(ctx).Iter()
		rows, err := iter.SliceMap()
		if err := iter.Close(); err != nil {
			return nil, err
		}
		return keysFrom(rows, "keyspace_name"), nil

	case familyZooKeeper:
		conn, err := p.zkConn(c)
		if err != nil {
			return nil, err
		}
		children, _, err := conn.Children("/")
		if err != nil {
			return nil, err
		}
		sort.Strings(children)
		return children, nil

	case familyMQTT:
		return mqttTopics(c)

	case familyNebula:
		sess, release, err := p.nebulaSession(c)
		if err != nil {
			return nil, err
		}
		defer release()
		spaces, err := sess.ShowSpaces()
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(spaces))
		for _, space := range spaces {
			if space.Name != "" {
				out = append(out, space.Name)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	return nil, nil
}

// nativeObjects 列出命名空间下的对象（表 / 图标签 / 节点路径）。
func (p *DB) nativeObjects(c Connection, database string) ([]Object, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch familyOf(c.Driver) {
	case familyCassandra:
		session, err := p.cassandraSession(c)
		if err != nil {
			return nil, err
		}
		var out []Object
		iter := session.Query("SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?", database).WithContext(ctx).Iter()
		tables, err := iter.SliceMap()
		if err := iter.Close(); err != nil {
			return nil, err
		}
		for _, name := range keysFrom(tables, "table_name") {
			out = append(out, Object{Name: name, Type: "table"})
		}
		vIter := session.Query("SELECT view_name FROM system_schema.views WHERE keyspace_name = ?", database).WithContext(ctx).Iter()
		views, err := vIter.SliceMap()
		if err := vIter.Close(); err != nil && err != gocql.ErrNotFound {
			return nil, err
		}
		for _, name := range keysFrom(views, "view_name") {
			out = append(out, Object{Name: name, Type: "view"})
		}
		return out, nil

	case familyZooKeeper:
		conn, err := p.zkConn(c)
		if err != nil {
			return nil, err
		}
		path := "/" + strings.Trim(database, "/")
		children, _, err := conn.Children(path)
		if err != nil {
			if err == zk.ErrNoNode {
				return nil, nil
			}
			return nil, err
		}
		sort.Strings(children)
		out := make([]Object, 0, len(children))
		for _, name := range children {
			out = append(out, Object{Name: name, Type: "node"})
		}
		return out, nil

	case familyMQTT:
		return nil, nil

	case familyNebula:
		if database == "" {
			return nil, nil
		}
		sess, release, err := p.nebulaSession(c)
		if err != nil {
			return nil, err
		}
		defer release()
		out := make([]Object, 0, 16)
		for _, stmt := range []string{"SHOW TAGS", "SHOW EDGES"} {
			raw, err := sess.ExecuteJson("USE `" + database + "`; " + stmt)
			if err != nil {
				continue
			}
			_, rows, err := nebulaJSONRows(raw)
			if err != nil {
				continue
			}
			typ := "tag"
			if stmt == "SHOW EDGES" {
				typ = "edge"
			}
			for _, row := range rows {
				if len(row) > 0 {
					out = append(out, Object{Name: fmt.Sprint(row[0]), Type: typ})
				}
			}
		}
		return out, nil
	}
	return nil, nil
}

// nativeExec 执行原生查询。
func (p *DB) nativeExec(c Connection, database, q string, max int) (*queryResult, error) {
	limit := clampRows(max)
	const maxRows = 5000
	if limit > maxRows {
		limit = maxRows
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch familyOf(c.Driver) {
	case familyCassandra:
		session, err := p.cassandraSession(c)
		if err != nil {
			return nil, err
		}
		iter := session.Query(q).WithContext(ctx).Iter()
		rows, err := iter.SliceMap()
		cols := iter.Columns()
		closeErr := iter.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		names := make([]string, 0, len(cols))
		for _, col := range cols {
			names = append(names, col.Name)
		}
		if len(rows) == 0 {
			if len(names) == 0 {
				return &queryResult{Kind: "exec"}, nil
			}
			return rowsResult(names, [][]any{}), nil
		}
		items := make([]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, row)
		}
		outCols, outRows := rowsFromObjects(items)
		if len(outRows) > limit {
			return &queryResult{Kind: "rows", Columns: outCols, Rows: outRows[:limit], Truncated: true}, nil
		}
		return rowsResult(outCols, outRows), nil

	case familyZooKeeper:
		conn, err := p.zkConn(c)
		if err != nil {
			return nil, err
		}
		return zkExec(conn, database, q, limit)

	case familyMQTT:
		client, err := newMQTTClient(c)
		if err != nil {
			return nil, err
		}
		defer client.Disconnect(250)
		return mqttExec(client, q, limit)

	case familyNebula:
		sess, release, err := p.nebulaSession(c)
		if err != nil {
			return nil, err
		}
		defer release()
		stmt := q
		if database != "" && !strings.Contains(strings.ToUpper(q), "USE ") {
			stmt = "USE `" + database + "`; " + q
		}
		raw, err := sess.ExecuteJson(stmt)
		if err != nil {
			return nil, err
		}
		cols, rows, err := nebulaJSONRows(raw)
		if err != nil {
			return nil, err
		}
		if len(rows) > limit {
			return &queryResult{Kind: "rows", Columns: cols, Rows: rows[:limit], Truncated: true}, nil
		}
		if len(cols) == 0 {
			return &queryResult{Kind: "exec"}, nil
		}
		return rowsResult(cols, rows), nil
	}
	return nil, fmt.Errorf("该类型暂不支持查询")
}

/* ---------- 客户端缓存 ---------- */

// nativeClient 取出（或创建）连接对应的原生客户端。
// 创建过程不持有全局锁，避免慢速网络连接阻塞其它类型。
func (p *DB) nativeClient(c Connection, factory func() (any, error)) (any, error) {
	p.nativeMu.Lock()
	if client, ok := p.native[c.ID]; ok {
		p.nativeMu.Unlock()
		return client, nil
	}
	p.nativeMu.Unlock()

	client, err := factory()
	if err != nil {
		return nil, err
	}
	p.nativeMu.Lock()
	if existing, ok := p.native[c.ID]; ok {
		p.nativeMu.Unlock()
		closeNativeClient(client)
		return existing, nil
	}
	p.native[c.ID] = client
	p.nativeMu.Unlock()
	return client, nil
}

func (p *DB) cassandraSession(c Connection) (*gocql.Session, error) {
	client, err := p.nativeClient(c, func() (any, error) {
		cluster := gocql.NewCluster(c.Host)
		cluster.Port = defPort(c)
		cluster.Timeout = 10 * time.Second
		cluster.ConnectTimeout = 10 * time.Second
		cluster.Consistency = gocql.LocalQuorum
		if c.User != "" {
			cluster.Authenticator = gocql.PasswordAuthenticator{Username: c.User, Password: c.Password}
		}
		if v := parseParams(c.Params)["proto"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cluster.ProtoVersion = n
			}
		}
		if v := parseParams(c.Params)["keyspace"]; v != "" {
			cluster.Keyspace = v
		}
		return cluster.CreateSession()
	})
	if err != nil {
		return nil, err
	}
	session, _ := client.(*gocql.Session)
	if session == nil {
		return nil, fmt.Errorf("Cassandra 客户端类型异常")
	}
	return session, nil
}

func (p *DB) zkConn(c Connection) (*zk.Conn, error) {
	client, err := p.nativeClient(c, func() (any, error) {
		addr := net.JoinHostPort(defaultHost(c), strconv.Itoa(defPort(c)))
		conn, _, err := zk.Connect([]string{addr}, 10*time.Second)
		return conn, err
	})
	if err != nil {
		return nil, err
	}
	conn, _ := client.(*zk.Conn)
	if conn == nil {
		return nil, fmt.Errorf("ZooKeeper 客户端类型异常")
	}
	return conn, nil
}

// quietNebulaLogger 屏蔽 nebula 客户端默认的标准输出日志。
type quietNebulaLogger struct{}

func (quietNebulaLogger) Info(string)  {}
func (quietNebulaLogger) Warn(string)  {}
func (quietNebulaLogger) Error(string) {}
func (quietNebulaLogger) Fatal(string) {}

// nebulaCreds 返回 Nebula 登录账号，未填写时使用默认的 root/nebula。
func nebulaCreds(c Connection) (string, string) {
	user := strings.TrimSpace(c.User)
	if user == "" {
		user = "root"
	}
	password := c.Password
	if password == "" && user == "root" {
		password = "nebula"
	}
	return user, password
}

// nebulaConnPool 返回不带图空间的连接池，用于 SHOW SPACES 等全局语句。
func (p *DB) nebulaConnPool(c Connection) (*nebula.ConnectionPool, error) {
	client, err := p.nativeClient(Connection{ID: c.ID + "|nebula-pool"}, func() (any, error) {
		conf := nebula.GetDefaultConf()
		conf.TimeOut = 10 * time.Second
		return nebula.NewConnectionPool(
			[]nebula.HostAddress{{Host: defaultHost(c), Port: defPort(c)}},
			conf, quietNebulaLogger{},
		)
	})
	if err != nil {
		return nil, err
	}
	pool, _ := client.(*nebula.ConnectionPool)
	if pool == nil {
		return nil, fmt.Errorf("Nebula 客户端类型异常")
	}
	return pool, nil
}

// nebulaSession 取一个不带图空间的会话，调用方必须执行返回的释放函数。
// 图空间通过语句中的 USE 切换，这样同一个会话即可覆盖全局与空间内操作。
func (p *DB) nebulaSession(c Connection) (*nebula.Session, func(), error) {
	pool, err := p.nebulaConnPool(c)
	if err != nil {
		return nil, nil, err
	}
	user, password := nebulaCreds(c)
	sess, err := pool.GetSession(user, password)
	if err != nil {
		return nil, nil, err
	}
	return sess, sess.Release, nil
}

// closeNativeClient 关闭单个原生客户端。
func closeNativeClient(client any) {
	switch t := client.(type) {
	case *gocql.Session:
		t.Close()
	case *zk.Conn:
		t.Close()
	case *nebula.ConnectionPool:
		t.Close()
	}
}

// closeNative 关闭某个连接下缓存的全部原生客户端。
func (p *DB) closeNative(connID string) {
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	for key, client := range p.native {
		if key != connID && !strings.HasPrefix(key, connID+"|") {
			continue
		}
		closeNativeClient(client)
		delete(p.native, key)
	}
}

/* ---------- 辅助 ---------- */

func defaultHost(c Connection) string {
	if strings.TrimSpace(c.Host) == "" {
		return "127.0.0.1"
	}
	return c.Host
}

// keysFrom 从 map 结果中提取指定字段。
func keysFrom(rows []map[string]any, key string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if v, ok := row[key]; ok && v != nil {
			out = append(out, fmt.Sprint(v))
		}
	}
	sort.Strings(out)
	return out
}

// nebulaJSONRows 解析 NebulaGraph 的 ExecuteJson 结果。
func nebulaJSONRows(raw []byte) ([]string, [][]any, error) {
	var root struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Results []struct {
			Columns []string `json:"columns"`
			Data    []struct {
				Row []any `json:"row"`
			} `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, fmt.Errorf("Nebula 响应解析失败: %w", err)
	}
	if len(root.Errors) > 0 {
		return nil, nil, fmt.Errorf("Nebula 执行失败: %s", root.Errors[0].Message)
	}
	if len(root.Results) == 0 {
		return nil, nil, nil
	}
	res := root.Results[len(root.Results)-1]
	rows := make([][]any, 0, len(res.Data))
	for _, item := range res.Data {
		row := make([]any, len(item.Row))
		for i, v := range item.Row {
			row[i] = jsonValue(v)
		}
		rows = append(rows, row)
	}
	cols := res.Columns
	if cols == nil {
		cols = []string{}
	}
	return cols, rows, nil
}

/* ---------- ZooKeeper 操作 ---------- */

// zkExec 支持「ls /path」「get /path」「stat /path」以及 JSON 形式的高级操作。
func zkExec(conn *zk.Conn, database, q string, limit int) (*queryResult, error) {
	trimmed := strings.TrimSpace(q)
	if strings.HasPrefix(trimmed, "{") {
		var req struct {
			Op    string `json:"op"`
			Path  string `json:"path"`
			Data  string `json:"data"`
			Force bool   `json:"force"`
		}
		if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
			return nil, fmt.Errorf("请求体必须是合法 JSON: %w", err)
		}
		path := req.Path
		if path == "" {
			path = "/"
		}
		switch strings.ToLower(req.Op) {
		case "create":
			created, err := conn.Create(path, []byte(req.Data), 0, zk.WorldACL(zk.PermAll))
			if err != nil {
				return nil, err
			}
			return rowsResult([]string{"created"}, [][]any{{created}}), nil
		case "delete":
			if err := conn.Delete(req.Path, -1); err != nil {
				return nil, err
			}
			return &queryResult{Kind: "exec", Affected: 1}, nil
		default:
			return zkRead(conn, path, req.Op, limit)
		}
	}

	fields := strings.Fields(trimmed)
	op := "ls"
	path := trimmed
	if len(fields) >= 2 {
		op = strings.ToLower(fields[0])
		path = fields[1]
	}
	if path == "" {
		path = "/" + strings.Trim(database, "/")
	}
	return zkRead(conn, path, op, limit)
}

func zkRead(conn *zk.Conn, path, op string, limit int) (*queryResult, error) {
	switch op {
	case "", "ls", "children":
		children, _, err := conn.Children(path)
		if err != nil {
			return nil, err
		}
		sort.Strings(children)
		rows := make([][]any, 0, len(children))
		for _, name := range children {
			if len(rows) >= limit {
				return &queryResult{Kind: "rows", Columns: []string{"path"}, Rows: rows, Truncated: true}, nil
			}
			rows = append(rows, []any{strings.TrimRight(path, "/") + "/" + name})
		}
		return rowsResult([]string{"path"}, rows), nil
	case "get":
		data, stat, err := conn.Get(path)
		if err != nil {
			return nil, err
		}
		return rowsResult([]string{"path", "value", "version", "dataLength"}, [][]any{{
			path, string(data), int64(stat.Version), int64(stat.DataLength),
		}}), nil
	case "stat":
		_, stat, err := conn.Get(path)
		if err != nil {
			return nil, err
		}
		return rowsResult([]string{"path", "version", "dataLength", "numChildren", "ephemeralOwner"}, [][]any{{
			path, int64(stat.Version), int64(stat.DataLength), int32(stat.NumChildren), fmt.Sprint(stat.EphemeralOwner),
		}}), nil
	default:
		return nil, fmt.Errorf("不支持的 ZooKeeper 操作：%s（支持 ls / get / stat / create / delete）", op)
	}
}

/* ---------- MQTT ---------- */

// newMQTTClient 构建一个临时 MQTT 客户端（调用方负责断开）。
func newMQTTClient(c Connection) (mqtt.Client, error) {
	scheme := "tcp"
	params := parseParams(c.Params)
	if v := params["scheme"]; v != "" {
		scheme = v
	} else if defPort(c) == 8883 {
		scheme = "ssl"
	}
	broker := fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(defaultHost(c), strconv.Itoa(defPort(c))))
	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID("devos-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetAutoReconnect(false)
	if c.User != "" {
		opts.SetUsername(c.User)
		opts.SetPassword(c.Password)
	}
	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(12 * time.Second) {
		return nil, fmt.Errorf("MQTT 连接超时")
	}
	if err := token.Error(); err != nil {
		return nil, err
	}
	return client, nil
}

// mqttTopics 通过短时订阅「#」收集活跃主题。
func mqttTopics(c Connection) ([]string, error) {
	params := parseParams(c.Params)
	if raw := strings.TrimSpace(params["topics"]); raw != "" {
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	}

	client, err := newMQTTClient(c)
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(250)

	var mu sync.Mutex
	seen := map[string]bool{}
	token := client.Subscribe("#", 0, func(_ mqtt.Client, msg mqtt.Message) {
		mu.Lock()
		defer mu.Unlock()
		if len(seen) < 500 {
			seen[msg.Topic()] = true
		}
	})
	if !token.WaitTimeout(5 * time.Second) {
		return nil, fmt.Errorf("MQTT 订阅超时")
	}
	if err := token.Error(); err != nil {
		return nil, err
	}
	time.Sleep(1500 * time.Millisecond)
	client.Unsubscribe("#")

	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(seen))
	for topic := range seen {
		out = append(out, topic)
	}
	sort.Strings(out)
	return out, nil
}

func mqttExec(client mqtt.Client, q string, limit int) (*queryResult, error) {
	var req struct {
		Op      string `json:"op"`
		Topic   string `json:"topic"`
		Payload string `json:"payload"`
		QoS     byte   `json:"qos"`
		Retain  bool   `json:"retain"`
		Count   int    `json:"count"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(q), &req); err != nil {
		return nil, fmt.Errorf(`请求格式应为 {"op":"publish","topic":"a/b","payload":"hi"}`)
	}
	switch strings.ToLower(req.Op) {
	case "publish":
		if req.Topic == "" {
			return nil, fmt.Errorf("发布消息需要提供 topic")
		}
		token := client.Publish(req.Topic, req.QoS, req.Retain, req.Payload)
		if !token.WaitTimeout(10 * time.Second) {
			return nil, fmt.Errorf("MQTT 发布超时")
		}
		if err := token.Error(); err != nil {
			return nil, err
		}
		return &queryResult{Kind: "exec", Affected: 1}, nil
	case "subscribe":
		if req.Topic == "" {
			return nil, fmt.Errorf("订阅需要提供 topic")
		}
		want := req.Count
		if want <= 0 || want > limit {
			want = limit
		}
		var mu sync.Mutex
		rows := make([][]any, 0, want)
		done := make(chan struct{})
		var once sync.Once
		token := client.Subscribe(req.Topic, req.QoS, func(_ mqtt.Client, msg mqtt.Message) {
			mu.Lock()
			rows = append(rows, []any{msg.Topic(), string(msg.Payload()), msg.Qos(), msg.Retained()})
			full := len(rows) >= want
			mu.Unlock()
			if full {
				once.Do(func() { close(done) })
			}
		})
		if !token.WaitTimeout(5 * time.Second) {
			return nil, fmt.Errorf("MQTT 订阅超时")
		}
		if err := token.Error(); err != nil {
			return nil, err
		}
		wait := req.Timeout
		if wait <= 0 || wait > 30 {
			wait = 5
		}
		select {
		case <-done:
		case <-time.After(time.Duration(wait) * time.Second):
		}
		client.Unsubscribe(req.Topic)
		mu.Lock()
		defer mu.Unlock()
		return rowsResult([]string{"topic", "payload", "qos", "retained"}, rows), nil
	case "unsubscribe":
		if req.Topic == "" {
			return nil, fmt.Errorf("取消订阅需要提供 topic")
		}
		client.Unsubscribe(req.Topic)
		return &queryResult{Kind: "exec"}, nil
	default:
		return nil, fmt.Errorf("不支持的 MQTT 操作：%s（支持 publish / subscribe / unsubscribe）", req.Op)
	}
}
