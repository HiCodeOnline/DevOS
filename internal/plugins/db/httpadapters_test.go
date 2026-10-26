package dbapp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// httpConn 构造指向测试服务器的 HTTP 族连接。
func httpConn(driver, url string) Connection {
	return Connection{ID: "t1", Driver: driver, Host: url, Params: "url=" + url}
}

func TestHTTPElasticsearchFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`))
		case r.URL.Path == "/_sql":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != "SELECT name FROM users" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"unexpected query"}`))
				return
			}
			_, _ = w.Write([]byte(`{"columns":[{"name":"name"}],"rows":[["alice"],["bob"]]}`))
		case r.URL.Path == "/users/_search":
			_, _ = w.Write([]byte(`{"hits":{"hits":[{"_id":"1","_source":{"name":"alice","age":30}}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New()
	conn := httpConn("elasticsearch", srv.URL)

	_, version, err := p.httpPing(conn)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if version != "8.13.0" {
		t.Fatalf("版本 = %q，期望 8.13.0", version)
	}

	res, err := p.httpQuery(conn, "", "SELECT name FROM users", 100)
	if err != nil {
		t.Fatalf("SQL 查询: %v", err)
	}
	if res.Kind != "rows" || len(res.Columns) != 1 || len(res.Rows) != 2 {
		t.Fatalf("SQL 结果不正确: %+v", res)
	}
	if res.Rows[0][0] != "alice" {
		t.Fatalf("首行 = %v，期望 alice", res.Rows[0])
	}

	res, err = p.httpQuery(conn, "users", `{"query":{"match_all":{}}}`, 100)
	if err != nil {
		t.Fatalf("检索请求: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		// 结果列按字典序排列，首列应为 _id。
		t.Fatalf("检索结果不正确: %+v", res)
	}
}

func TestHTTPEtcdFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/status":
			_, _ = w.Write([]byte(`{"version":"3.5.12"}`))
		case "/v3/kv/range":
			_, _ = w.Write([]byte(`{"kvs":[{"key":"` + base64.StdEncoding.EncodeToString([]byte("/app/a")) +
				`","value":"` + base64.StdEncoding.EncodeToString([]byte("1")) + `"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New()
	conn := httpConn("etcd", srv.URL)

	_, version, err := p.httpPing(conn)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if version != "3.5.12" {
		t.Fatalf("版本 = %q，期望 3.5.12", version)
	}

	names, err := p.httpDatabases(conn)
	if err != nil {
		t.Fatalf("命名空间: %v", err)
	}
	if len(names) != 1 || names[0] != "keys" {
		t.Fatalf("etcd 命名空间不正确: %v", names)
	}

	res, err := p.httpQuery(conn, "", "/app/", 100)
	if err != nil {
		t.Fatalf("读取键: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "/app/a" || res.Rows[0][1] != "1" {
		t.Fatalf("键值结果不正确: %+v", res)
	}
}

func TestHTTPClientBase(t *testing.T) {
	cl, err := newHTTPClient(Connection{
		Driver: "elasticsearch", Host: "127.0.0.1", Port: 9200,
		Params: "basePath=/es",
	})
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}
	if cl.base != "http://127.0.0.1:9200/es" {
		t.Fatalf("base = %q", cl.base)
	}
	if cl.kind != "basic" {
		t.Fatalf("默认鉴权 = %q，期望 basic", cl.kind)
	}

	cl, err = newHTTPClient(Connection{
		Driver: "consul", Host: "https://consul.example.com", Params: "token=abc",
	})
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}
	if cl.base != "https://consul.example.com" || cl.kind != "bearer" || cl.token != "abc" {
		t.Fatalf("URL 显式协议时解析不正确: %+v", cl)
	}
}

func TestHTTPReadOnlyGuard(t *testing.T) {
	cases := map[string]bool{
		`{"method":"GET","path":"/_cluster/health"}`: false,
		`{"method":"post","path":"/idx/_doc"}`:       true,
		`{"method":"DELETE","path":"/idx/_doc/1"}`:   true,
		"SELECT * FROM t":                            false,
		"DELETE FROM t":                              true,
		"{\"query\":{\"match_all\":{}}}":             false,
	}
	for q, want := range cases {
		if got := httpRequestIsWrite(q); got != want {
			t.Fatalf("httpRequestIsWrite(%q) = %v，期望 %v", q, got, want)
		}
	}
}
