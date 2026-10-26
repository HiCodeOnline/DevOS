package dbapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 适配器共用的默认行数上限。
const httpDefaultRows = 1000

func clampRows(max int) int {
	if max <= 0 {
		return httpDefaultRows
	}
	if max > 10000 {
		return 10000
	}
	return max
}

func pathEscape(s string) string { return url.PathEscape(s) }

// genericRows 从任意 JSON 响应中提取二维结果集。
func genericRows(v any) ([]string, [][]any, bool) {
	switch t := v.(type) {
	case []any:
		cols, rows := rowsFromObjects(t)
		return cols, rows, true
	case map[string]any:
		for _, key := range []string{"data", "results", "rows", "items", "records", "documents", "hits", "result", "kvs", "values"} {
			inner, ok := t[key]
			if !ok {
				continue
			}
			if cols, rows, ok := genericRows(inner); ok {
				return cols, rows, true
			}
		}
		cols, rows := rowsFromObjects([]any{t})
		return cols, rows, true
	}
	return nil, nil, false
}

func rowsResult(cols []string, rows [][]any) *queryResult {
	return &queryResult{Kind: "rows", Columns: cols, Rows: rows}
}

func rawBodyResult(raw []byte) *queryResult {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return &queryResult{Kind: "exec"}
	}
	var parsed any
	if json.Unmarshal(raw, &parsed) == nil {
		if cols, rows, ok := genericRows(parsed); ok {
			return rowsResult(cols, rows)
		}
	}
	return rowsResult([]string{"body"}, [][]any{{text}})
}

// httpProbe 按类型探测连接，返回版本号等标识。
func httpProbe(ctx context.Context, cl *httpClient, id string) (string, error) {
	switch id {
	case "elasticsearch", "easysearch":
		var root map[string]any
		if err := cl.getJSON(ctx, "/", nil, &root); err != nil {
			return "", err
		}
		if v, ok := dig(root, "version", "number").(string); ok && v != "" {
			return v, nil
		}
		if v, ok := root["tagline"].(string); ok {
			return v, nil
		}
		return "", nil

	case "meilisearch":
		var root map[string]any
		if err := cl.getJSON(ctx, "/version", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["pkgVersion"]), nil

	case "solr":
		var root map[string]any
		_ = cl.getJSON(ctx, "/solr/admin/info/system", url.Values{"wt": {"json"}}, &root)
		if v, ok := dig(root, "system", "lucene", "solr-specification").(string); ok {
			return v, nil
		}
		return "Solr", nil

	case "qdrant":
		var root map[string]any
		if err := cl.getJSON(ctx, "/", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["version"]), nil

	case "milvus":
		var root map[string]any
		if err := cl.postJSON(ctx, "/v2/vectordb/collections/list", map[string]any{}, &root); err != nil {
			return "", err
		}
		if code, _ := root["code"].(float64); code != 0 && code != 200 {
			return "", fmt.Errorf("Milvus 返回异常: %v", root["message"])
		}
		return "Milvus REST v2", nil

	case "weaviate":
		var root map[string]any
		if err := cl.getJSON(ctx, "/v1/meta", nil, &root); err != nil {
			return "", err
		}
		if v, ok := root["version"].(string); ok {
			return v, nil
		}
		return "Weaviate", nil

	case "chromadb":
		if status, _, raw, err := cl.do(ctx, http.MethodGet, "/api/v1/heartbeat", nil, nil, nil); err == nil && status < 300 {
			var root map[string]any
			_ = cl.getJSON(ctx, "/api/v1/version", nil, &root)
			if v, ok := root["version"].(string); ok {
				return v, nil
			}
			return "Chroma v1", nil
		} else if err == nil && status == http.StatusNotFound {
			if _, _, _, err := cl.do(ctx, http.MethodGet, "/api/v2/heartbeat", nil, nil, nil); err == nil {
				return "Chroma v2", nil
			}
			return "", statusError(status, raw)
		} else if err != nil {
			return "", err
		}
		return "", statusError(404, nil)

	case "neo4j":
		var root map[string]any
		if err := cl.getJSON(ctx, "/", nil, &root); err != nil {
			return "", err
		}
		if v, ok := root["neo4j_version"].(string); ok {
			return v, nil
		}
		return "Neo4j", nil

	case "couchdb":
		var root map[string]any
		if err := cl.getJSON(ctx, "/", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["version"]), nil

	case "influxdb":
		status, headers, raw, err := cl.do(ctx, http.MethodGet, "/ping", nil, nil, nil)
		if err != nil {
			return "", err
		}
		if status >= 300 {
			return "", statusError(status, raw)
		}
		if v := headers.Get("X-Influxdb-Version"); v != "" {
			return v, nil
		}
		var root map[string]any
		_ = cl.getJSON(ctx, "/health", nil, &root)
		return fmt.Sprint(root["version"]), nil

	case "influxdb3":
		var root map[string]any
		if err := cl.getJSON(ctx, "/health", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["version"]), nil

	case "victoriametrics":
		status, _, raw, err := cl.do(ctx, http.MethodGet, "/health", nil, nil, nil)
		if err != nil {
			return "", err
		}
		if status >= 300 {
			return "", statusError(status, raw)
		}
		var root map[string]any
		if cl.getJSON(ctx, "/api/v1/status/buildinfo", nil, &root) == nil {
			if v, ok := root["version"].(string); ok {
				return v, nil
			}
		}
		return "VictoriaMetrics", nil

	case "tdengine":
		res, err := tdengineExec(ctx, cl, "SELECT server_version()", 1)
		if err != nil {
			return "", err
		}
		if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
			return fmt.Sprint(res.Rows[0][0]), nil
		}
		return "TDengine", nil

	case "iotdb":
		status, _, raw, err := cl.do(ctx, http.MethodGet, "/rest/v1/ping", nil, nil, nil)
		if err != nil {
			return "", err
		}
		if status >= 300 {
			return "", statusError(status, raw)
		}
		var root map[string]any
		if cl.getJSON(ctx, "/rest/v1/version", nil, &root) == nil {
			if v, ok := root["version"].(string); ok {
				return v, nil
			}
		}
		return "Apache IoTDB", nil

	case "rqlite":
		var root map[string]any
		if err := cl.getJSON(ctx, "/status", nil, &root); err != nil {
			return "", err
		}
		if v := dig(root, "build", "version"); v != nil {
			return fmt.Sprint(v), nil
		}
		return "rqlite", nil

	case "turso":
		if _, err := tursoExec(ctx, cl, "SELECT 1", 1); err != nil {
			return "", err
		}
		return "libSQL HTTP", nil

	case "cloudflare-d1":
		if _, err := d1Exec(ctx, cl, "SELECT 1", 1); err != nil {
			return "", err
		}
		return "Cloudflare D1", nil

	case "databricks":
		var root map[string]any
		if err := cl.postJSON(ctx, "/api/2.0/sql/statements", map[string]any{
			"statement":    "SELECT 1",
			"warehouse_id": cl.params["warehouse"],
			"wait_timeout": "30s",
		}, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(dig(root, "manifest", "format")), nil

	case "bigquery":
		project := cl.params["project"]
		if project == "" {
			return "", fmt.Errorf("BigQuery 需要在附加参数中提供 project")
		}
		var root map[string]any
		if err := cl.getJSON(ctx, "/projects/"+pathEscape(project), nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["friendlyName"]), nil

	case "salesforce":
		var root []any
		if err := cl.getJSON(ctx, "/services/data/", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(len(root)) + " API 版本", nil

	case "kylin":
		var root []any
		if err := cl.getJSON(ctx, "/kylin/api/projects", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(len(root)) + " 项目", nil

	case "hbase":
		status, _, raw, err := cl.do(ctx, http.MethodGet, "/status/cluster", nil, nil, nil)
		if err != nil {
			return "", err
		}
		if status >= 300 {
			return "", statusError(status, raw)
		}
		return "HBase REST", nil

	case "etcd":
		var root map[string]any
		if err := cl.getJSON(ctx, "/v3/status", nil, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["version"]), nil

	case "consul":
		status, _, raw, err := cl.do(ctx, http.MethodGet, "/v1/status/leader", nil, nil, nil)
		if err != nil {
			return "", err
		}
		if status >= 300 {
			return "", statusError(status, raw)
		}
		leader := strings.Trim(strings.TrimSpace(string(raw)), `"`)
		if leader == "" {
			return "Consul", nil
		}
		return "leader: " + leader, nil

	case "nacos":
		for _, p := range []string{"/nacos/v1/console/health/liveness", "/nacos/v1/console/health/readiness"} {
			status, _, raw, err := cl.do(ctx, http.MethodGet, p, nil, nil, nil)
			if err != nil {
				return "", err
			}
			if status < 300 {
				return "Nacos", nil
			}
			_ = raw
		}
		return "", fmt.Errorf("Nacos 健康检查失败")

	case "iris":
		var root map[string]any
		if err := cl.getJSON(ctx, "/api/atelier/", nil, &root); err != nil {
			return "", err
		}
		if v, ok := dig(root, "status", "version").(string); ok {
			return v, nil
		}
		return "InterSystems IRIS", nil

	case "ignite":
		var root map[string]any
		if err := cl.getJSON(ctx, "/ignite", url.Values{"cmd": {"version"}}, &root); err != nil {
			return "", err
		}
		return fmt.Sprint(root["version"]), nil

	case "dynamodb":
		if _, err := dynamoRequest(ctx, cl, "ListTables", map[string]any{"Limit": 1}); err != nil {
			return "", err
		}
		return "DynamoDB", nil

	case "mq":
		var root any
		if err := cl.getJSON(ctx, "/topics", nil, &root); err != nil {
			return "", err
		}
		cols, rows, _ := genericRows(root)
		return fmt.Sprintf("%d 个主题 / %d 列", len(rows), len(cols)), nil

	default:
		// 通用探测：只要能建立 HTTP 会话并得到非 5xx 响应即视为可达。
		status, _, raw, err := cl.do(ctx, http.MethodGet, "/", nil, nil, nil)
		if err != nil {
			return "", err
		}
		if status >= 500 {
			return "", statusError(status, raw)
		}
		return fmt.Sprintf("HTTP %d", status), nil
	}
}

// httpListDatabases 列出命名空间（库/索引/集合/主题等）。
func httpListDatabases(ctx context.Context, cl *httpClient, id string) ([]string, error) {
	switch id {
	case "elasticsearch", "easysearch":
		var root []map[string]any
		if err := cl.getJSON(ctx, "/_cat/indices", url.Values{"format": {"json"}, "h": {"index"}, "s": {"index"}}, &root); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(root))
		for _, item := range root {
			if name, ok := item["index"].(string); ok {
				out = append(out, name)
			}
		}
		return out, nil

	case "meilisearch":
		var root map[string]any
		if err := cl.getJSON(ctx, "/indexes", url.Values{"limit": {"1000"}}, &root); err != nil {
			return nil, err
		}
		return stringList(root["results"], "uid", "name"), nil

	case "solr":
		var root map[string]any
		if err := cl.getJSON(ctx, "/solr/admin/cores", url.Values{"action": {"STATUS"}, "wt": {"json"}}, &root); err != nil {
			return nil, err
		}
		out := make([]string, 0, 8)
		if status, ok := root["status"].(map[string]any); ok {
			for name := range status {
				out = append(out, name)
			}
		}
		return out, nil

	case "qdrant":
		var root map[string]any
		if err := cl.getJSON(ctx, "/collections", nil, &root); err != nil {
			return nil, err
		}
		return stringList(dig(root, "result", "collections"), "name"), nil

	case "milvus":
		var root map[string]any
		if err := cl.postJSON(ctx, "/v2/vectordb/collections/list", map[string]any{}, &root); err != nil {
			return nil, err
		}
		return stringList(root["data"], "collectionName", "name"), nil

	case "weaviate":
		var root map[string]any
		if err := cl.getJSON(ctx, "/v1/schema", nil, &root); err != nil {
			return nil, err
		}
		return stringList(root["classes"], "class", "name"), nil

	case "chromadb":
		var root []map[string]any
		if err := cl.getJSON(ctx, "/api/v1/collections", nil, &root); err == nil {
			out := make([]string, 0, len(root))
			for _, item := range root {
				if name, ok := item["name"].(string); ok {
					out = append(out, name)
				}
			}
			return out, nil
		}
		var root2 map[string]any
		if err := cl.getJSON(ctx, "/api/v2/tenants/default_tenant/databases/default_database/collections", nil, &root2); err != nil {
			return nil, err
		}
		return stringList(root2["collections"], "name"), nil

	case "neo4j":
		db := cl.params["database"]
		if db == "" {
			db = "neo4j"
		}
		var labels []string
		if err := cl.getJSON(ctx, "/db/"+pathEscape(db)+"/labels", nil, &labels); err == nil && len(labels) > 0 {
			return labels, nil
		}
		var old []string
		if err := cl.getJSON(ctx, "/db/data/labels", nil, &old); err == nil {
			return old, nil
		}
		return []string{db}, nil

	case "couchdb":
		var root []string
		if err := cl.getJSON(ctx, "/_all_dbs", nil, &root); err != nil {
			return nil, err
		}
		return root, nil

	case "influxdb":
		if strings.EqualFold(cl.params["version"], "2") {
			var root map[string]any
			_ = cl.getJSON(ctx, "/api/v2/buckets", url.Values{"org": {cl.params["org"]}, "limit": {"1000"}}, &root)
			if buckets := stringList(root["buckets"], "name"); len(buckets) > 0 {
				return buckets, nil
			}
		}
		var v1 struct {
			Results []struct {
				Series []struct {
					Values [][]any `json:"values"`
				} `json:"series"`
			} `json:"results"`
		}
		if err := cl.getJSON(ctx, "/query", url.Values{"q": {"SHOW DATABASES"}}, &v1); err != nil {
			return nil, err
		}
		out := make([]string, 0, 8)
		for _, res := range v1.Results {
			for _, series := range res.Series {
				for _, row := range series.Values {
					if len(row) > 0 {
						out = append(out, fmt.Sprint(row[0]))
					}
				}
			}
		}
		return out, nil

	case "influxdb3":
		db := cl.params["database"]
		if db == "" {
			db = "_internal"
		}
		var root any
		if err := cl.postJSON(ctx, "/api/v3/query_sql", map[string]any{"db": db, "q": "SHOW DATABASES", "format": "json"}, &root); err == nil {
			if arr, ok := root.([]any); ok {
				out := make([]string, 0, len(arr))
				for _, item := range arr {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					for _, key := range []string{"database", "database_name", "iox::database", "name"} {
						if v, ok := m[key].(string); ok && v != "" {
							out = append(out, v)
							break
						}
					}
				}
				if len(out) > 0 {
					return out, nil
				}
			}
		}
		return []string{"public"}, nil

	case "victoriametrics":
		var root map[string]any
		if err := cl.getJSON(ctx, "/api/v1/label/__name__/values", nil, &root); err != nil {
			return nil, err
		}
		if arr, ok := root["data"].([]any); ok {
			out := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out, nil
		}
		return nil, nil

	case "tdengine":
		res, err := tdengineExec(ctx, cl, "SHOW DATABASES", 200)
		if err != nil {
			return nil, err
		}
		return columnValues(res, 0), nil

	case "iotdb":
		res, err := iotdbExec(ctx, cl, "SHOW DATABASES", 200)
		if err != nil {
			return nil, err
		}
		return columnValues(res, 0), nil

	case "rqlite":
		return []string{"primary"}, nil

	case "turso":
		return []string{"main"}, nil

	case "cloudflare-d1":
		name := cl.params["database"]
		if name == "" {
			name = "d1"
		}
		return []string{name}, nil

	case "databricks":
		name := cl.params["catalog"]
		if name == "" {
			name = "main"
		}
		return []string{name}, nil

	case "bigquery":
		project := cl.params["project"]
		if project == "" {
			return nil, fmt.Errorf("BigQuery 需要在附加参数中提供 project")
		}
		return []string{project}, nil

	case "salesforce":
		return []string{"sobjects"}, nil

	case "kylin":
		var root []map[string]any
		if err := cl.getJSON(ctx, "/kylin/api/projects", nil, &root); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(root))
		for _, item := range root {
			if name, ok := item["name"].(string); ok {
				out = append(out, name)
			}
		}
		return out, nil

	case "hbase":
		var root []string
		if err := cl.getJSON(ctx, "/", nil, &root); err != nil {
			return nil, err
		}
		return root, nil

	case "etcd":
		return []string{"keys"}, nil

	case "consul":
		var root []string
		if err := cl.getJSON(ctx, "/v1/catalog/datacenters", nil, &root); err != nil {
			return nil, err
		}
		return root, nil

	case "nacos":
		var root map[string]any
		if err := cl.getJSON(ctx, "/nacos/v1/console/namespaces", nil, &root); err != nil {
			return nil, err
		}
		return stringList(root["data"], "namespace", "namespaceShowName"), nil

	case "iris":
		ns := cl.params["namespace"]
		if ns == "" {
			ns = "USER"
		}
		return []string{ns}, nil

	case "ignite":
		var root map[string]any
		if err := cl.getJSON(ctx, "/ignite", url.Values{"cmd": {"cacheNames"}}, &root); err != nil {
			return nil, err
		}
		return stringList(root["cacheNames"]), nil

	case "dynamodb":
		root, err := dynamoRequest(ctx, cl, "ListTables", map[string]any{"Limit": 200})
		if err != nil {
			return nil, err
		}
		return stringList(root["TableNames"]), nil

	case "mq":
		var root any
		if err := cl.getJSON(ctx, "/topics", nil, &root); err != nil {
			return nil, err
		}
		return stringList(root), nil

	default:
		return []string{}, nil
	}
}

// httpListObjects 列出命名空间下的对象；HTTP 适配器默认不展示子对象。
func httpListObjects(ctx context.Context, cl *httpClient, id, database string) ([]Object, error) {
	switch id {
	case "hbase":
		var root []string
		if err := cl.getJSON(ctx, "/"+pathEscape(database)+"/schema", nil, &root); err == nil {
			out := make([]Object, 0, len(root))
			for _, name := range root {
				out = append(out, Object{Name: name, Type: "table"})
			}
			return out, nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}

// httpExec 执行适配器查询。
func httpExec(ctx context.Context, cl *httpClient, id, database, q string, max int) (*queryResult, error) {
	limit := clampRows(max)
	switch id {
	case "elasticsearch", "easysearch":
		return esExec(ctx, cl, database, q, limit)
	case "meilisearch":
		return meiliExec(ctx, cl, database, q, limit)
	case "solr":
		return solrExec(ctx, cl, database, q, limit)
	case "qdrant":
		return qdrantExec(ctx, cl, database, q, limit)
	case "weaviate":
		var root map[string]any
		if err := cl.postJSON(ctx, "/v1/graphql", map[string]any{"query": q}, &root); err != nil {
			return nil, err
		}
		if errs, ok := root["errors"].([]any); ok && len(errs) > 0 {
			return nil, fmt.Errorf("GraphQL 执行失败: %v", errs[0])
		}
		if cols, rows, ok := genericRows(root["data"]); ok {
			return rowsResult(cols, rows), nil
		}
		return rowsResult([]string{"body"}, [][]any{{fmt.Sprint(root["data"])}}), nil
	case "milvus":
		return milvusExec(ctx, cl, database, q, limit)
	case "chromadb":
		return chromaExec(ctx, cl, database, q, limit)
	case "neo4j":
		return neo4jExec(ctx, cl, database, q, limit)
	case "couchdb":
		return couchExec(ctx, cl, database, q, limit)
	case "influxdb":
		return influxExec(ctx, cl, database, q, limit)
	case "influxdb3":
		return influx3Exec(ctx, cl, database, q, limit)
	case "victoriametrics":
		var root map[string]any
		if err := cl.getJSON(ctx, "/api/v1/query", url.Values{"query": {q}}, &root); err != nil {
			return nil, err
		}
		if cols, rows, ok := genericRows(dig(root, "data", "result")); ok {
			return rowsResult(cols, rows), nil
		}
		return nil, fmt.Errorf("VictoriaMetrics 未返回结果")
	case "tdengine":
		return tdengineExec(ctx, cl, q, limit)
	case "iotdb":
		return iotdbExec(ctx, cl, q, limit)
	case "rqlite":
		return rqliteExec(ctx, cl, q, limit)
	case "turso":
		return tursoExec(ctx, cl, q, limit)
	case "cloudflare-d1":
		return d1Exec(ctx, cl, q, limit)
	case "databricks":
		return databricksExec(ctx, cl, q, limit)
	case "bigquery":
		return bigqueryExec(ctx, cl, q, limit)
	case "salesforce":
		return salesforceExec(ctx, cl, q, limit)
	case "kylin":
		return kylinExec(ctx, cl, q, limit)
	case "hbase":
		return hbaseExec(ctx, cl, database, q, limit)
	case "etcd":
		return etcdExec(ctx, cl, q, limit)
	case "consul":
		return consulExec(ctx, cl, q, limit)
	case "nacos":
		return nacosExec(ctx, cl, q, limit)
	case "iris":
		return irisExec(ctx, cl, database, q, limit)
	case "ignite":
		return igniteExec(ctx, cl, q, limit)
	case "dynamodb":
		return dynamoExec(ctx, cl, q, limit)
	case "spanner":
		return spannerExec(ctx, cl, database, q, limit)
	case "mq":
		return mqExec(ctx, cl, database, q, limit)
	default:
		return genericHTTPExec(ctx, cl, q)
	}
}

func columnValues(res *queryResult, idx int) []string {
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if idx < len(row) {
			out = append(out, fmt.Sprint(row[idx]))
		}
	}
	return out
}

/* ---------- 各适配器实现 ---------- */

func esExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		var body any
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("检索语句必须是合法 JSON: %w", err)
		}
		path := "/_search"
		if ns != "" {
			path = "/" + pathEscape(ns) + "/_search"
		}
		var root map[string]any
		if err := cl.postJSON(ctx, path, body, &root); err != nil {
			return nil, err
		}
		hits := firstArray(root, []string{"hits", "hits"})
		items := make([]any, 0, len(hits))
		for _, h := range hits {
			hit, ok := h.(map[string]any)
			if !ok {
				continue
			}
			src, _ := hit["_source"].(map[string]any)
			if src == nil {
				src = map[string]any{}
			}
			if id, ok := hit["_id"]; ok {
				src["_id"] = id
			}
			items = append(items, src)
		}
		cols, rows := rowsFromObjects(items)
		if len(rows) > limit {
			rows = rows[:limit]
		}
		return rowsResult(cols, rows), nil
	}
	// 其余语句走 Elasticsearch SQL。
	var root map[string]any
	if err := cl.postJSON(ctx, "/_sql?format=json", map[string]any{"query": q, "fetch_size": limit}, &root); err != nil {
		return nil, err
	}
	return esSQLResult(root, limit)
}

func esSQLResult(root map[string]any, limit int) (*queryResult, error) {
	cols := make([]string, 0, 8)
	if rawCols, ok := root["columns"].([]any); ok {
		for _, c := range rawCols {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			cols = append(cols, fmt.Sprint(m["name"]))
		}
	}
	rows := make([][]any, 0, 16)
	if rawRows, ok := root["rows"].([]any); ok {
		for _, r := range rawRows {
			if len(rows) >= limit {
				return &queryResult{Kind: "rows", Columns: cols, Rows: rows, Truncated: true}, nil
			}
			arr, ok := r.([]any)
			if !ok {
				continue
			}
			row := make([]any, len(arr))
			for i, v := range arr {
				row[i] = jsonValue(v)
			}
			rows = append(rows, row)
		}
	}
	if len(cols) == 0 && len(rows) == 0 {
		return nil, fmt.Errorf("未返回结果集: %v", root["error"])
	}
	return rowsResult(cols, rows), nil
}

func meiliExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	if ns == "" {
		return nil, fmt.Errorf("请先选择一个索引")
	}
	var body any
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("检索语句必须是合法 JSON: %w", err)
		}
	} else {
		body = map[string]any{"q": q, "limit": limit}
	}
	var root map[string]any
	if err := cl.postJSON(ctx, "/indexes/"+pathEscape(ns)+"/search", body, &root); err != nil {
		return nil, err
	}
	cols, rows := rowsFromObjects(firstArray(root, []string{"hits"}))
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rowsResult(cols, rows), nil
}

func solrExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	if ns == "" {
		return nil, fmt.Errorf("请先选择一个 Core")
	}
	query := url.Values{"wt": {"json"}, "rows": {strconv.Itoa(limit)}}
	if strings.TrimSpace(q) != "" {
		query.Set("q", q)
	} else {
		query.Set("q", "*:*")
	}
	var root map[string]any
	if err := cl.getJSON(ctx, "/solr/"+pathEscape(ns)+"/select", query, &root); err != nil {
		return nil, err
	}
	cols, rows := rowsFromObjects(firstArray(root, []string{"response", "docs"}))
	return rowsResult(cols, rows), nil
}

func qdrantExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	if ns == "" {
		return nil, fmt.Errorf("请先选择一个集合")
	}
	var body any
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("请求体必须是合法 JSON: %w", err)
		}
	} else {
		body = map[string]any{"limit": limit, "with_payload": true}
	}
	var root map[string]any
	if err := cl.postJSON(ctx, "/collections/"+pathEscape(ns)+"/points/scroll", body, &root); err != nil {
		return nil, err
	}
	cols, rows := rowsFromObjects(firstArray(root, []string{"result", "points"}))
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rowsResult(cols, rows), nil
}

func milvusExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	var root map[string]any
	var err error
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		err = cl.postJSON(ctx, "/v2/vectordb/entities/query", json.RawMessage(q), &root)
	} else {
		err = cl.postJSON(ctx, "/v2/vectordb/entities/query", map[string]any{
			"collectionName": ns,
			"filter":         q,
			"limit":          limit,
			"outputFields":   []string{"*"},
		}, &root)
	}
	if err != nil {
		return nil, err
	}
	data, _ := root["data"].([]any)
	cols, rows := rowsFromObjects(data)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rowsResult(cols, rows), nil
}

func chromaExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	if ns == "" {
		return nil, fmt.Errorf("请先选择一个集合")
	}
	var body any
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("请求体必须是合法 JSON: %w", err)
		}
	} else {
		body = map[string]any{"limit": limit, "include": []string{"documents", "metadatas"}}
	}
	var root map[string]any
	if err := cl.postJSON(ctx, "/api/v1/collections/"+pathEscape(ns)+"/get", body, &root); err != nil {
		return nil, err
	}
	ids, _ := root["ids"].([]any)
	docs, _ := root["documents"].([]any)
	metas, _ := root["metadatas"].([]any)
	items := make([]any, 0, len(ids))
	for i, id := range ids {
		row := map[string]any{"id": id}
		if i < len(docs) {
			row["document"] = docs[i]
		}
		if i < len(metas) {
			row["metadata"] = metas[i]
		}
		items = append(items, row)
	}
	cols, rows := rowsFromObjects(items)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rowsResult(cols, rows), nil
}

func neo4jExec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	db := database
	if db == "" {
		db = cl.params["database"]
	}
	if db == "" {
		db = "neo4j"
	}
	body := map[string]any{"statements": []any{map[string]any{"statement": q}}}
	var root map[string]any
	path := "/db/" + pathEscape(db) + "/tx/commit"
	if err := cl.postJSON(ctx, path, body, &root); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			if err := cl.postJSON(ctx, "/db/data/transaction/commit", body, &root); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	if errs, ok := root["errors"].([]any); ok && len(errs) > 0 {
		first, _ := errs[0].(map[string]any)
		return nil, fmt.Errorf("Cypher 执行失败: %v", first["message"])
	}
	results, _ := root["results"].([]any)
	if len(results) == 0 {
		return &queryResult{Kind: "exec"}, nil
	}
	res, _ := results[0].(map[string]any)
	cols := stringList(res["columns"])
	rows := make([][]any, 0, 16)
	if data, ok := res["data"].([]any); ok {
		for _, d := range data {
			if len(rows) >= limit {
				return &queryResult{Kind: "rows", Columns: cols, Rows: rows, Truncated: true}, nil
			}
			item, _ := d.(map[string]any)
			if rowArr, ok := item["row"].([]any); ok {
				row := make([]any, len(rowArr))
				for i, v := range rowArr {
					row[i] = jsonValue(v)
				}
				rows = append(rows, row)
			}
		}
	}
	if cols == nil {
		cols = []string{}
	}
	return rowsResult(cols, rows), nil
}

func couchExec(ctx context.Context, cl *httpClient, ns, q string, limit int) (*queryResult, error) {
	if ns == "" {
		return nil, fmt.Errorf("请先选择一个数据库")
	}
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		var body any
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("请求体必须是合法 JSON: %w", err)
		}
		var root map[string]any
		if err := cl.postJSON(ctx, "/"+pathEscape(ns)+"/_find", body, &root); err != nil {
			return nil, err
		}
		cols, rows := rowsFromObjects(firstArray(root, []string{"docs"}))
		return rowsResult(cols, rows), nil
	}
	var root map[string]any
	query := url.Values{"include_docs": {"true"}, "limit": {strconv.Itoa(limit)}}
	if err := cl.getJSON(ctx, "/"+pathEscape(ns)+"/_all_docs", query, &root); err != nil {
		return nil, err
	}
	items := make([]any, 0, 16)
	if rowsArr, ok := root["rows"].([]any); ok {
		for _, r := range rowsArr {
			m, _ := r.(map[string]any)
			if doc, ok := m["doc"].(map[string]any); ok {
				items = append(items, doc)
			} else {
				items = append(items, m)
			}
		}
	}
	cols, rows := rowsFromObjects(items)
	return rowsResult(cols, rows), nil
}

func influxExec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	if strings.EqualFold(cl.params["version"], "2") {
		status, _, raw, err := cl.do(ctx, http.MethodPost, "/api/v2/query", url.Values{"org": {cl.params["org"]}}, q, map[string]string{
			"Accept":       "application/csv",
			"Content-Type": "application/vnd.flux",
		})
		if err != nil {
			return nil, err
		}
		if status >= 300 {
			return nil, statusError(status, raw)
		}
		return parseFluxCSV(raw, limit)
	}
	query := url.Values{"q": {q}}
	if database != "" {
		query.Set("db", database)
	}
	if epoch := cl.params["epoch"]; epoch != "" {
		query.Set("epoch", epoch)
	}
	var root map[string]any
	if err := cl.getJSON(ctx, "/query", query, &root); err != nil {
		return nil, err
	}
	results, _ := root["results"].([]any)
	var cols []string
	var rows [][]any
	multi := false
	for _, res := range results {
		m, _ := res.(map[string]any)
		if errMsg, ok := m["error"].(string); ok && errMsg != "" {
			return nil, fmt.Errorf("InfluxDB 查询失败: %s", errMsg)
		}
		series, _ := m["series"].([]any)
		if len(series) > 1 {
			multi = true
		}
		for _, s := range series {
			sm, _ := s.(map[string]any)
			if cols == nil {
				cols = stringList(sm["columns"])
			}
			values, _ := sm["values"].([]any)
			_, sub, _ := arrayRows(cols, values, 0)
			if multi {
				for _, row := range sub {
					withSeries := append([]any{fmt.Sprint(sm["name"])}, row...)
					rows = append(rows, withSeries)
				}
			} else {
				rows = append(rows, sub...)
			}
		}
	}
	if multi {
		cols = append([]string{"series"}, cols...)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	if cols == nil {
		cols = []string{}
	}
	return rowsResult(cols, rows), nil
}

// parseFluxCSV 解析 InfluxDB v2 的 CSV 响应。
func parseFluxCSV(raw []byte, limit int) (*queryResult, error) {
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(string(raw))))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("Flux 结果解析失败: %w", err)
	}
	var cols []string
	rows := make([][]any, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		if strings.HasPrefix(record[0], "#") {
			continue
		}
		if cols == nil {
			cols = append([]string{}, record...)
			continue
		}
		if len(rows) >= limit {
			return &queryResult{Kind: "rows", Columns: cols, Rows: rows, Truncated: true}, nil
		}
		row := make([]any, len(record))
		for i, v := range record {
			row[i] = v
		}
		rows = append(rows, row)
	}
	if cols == nil {
		cols = []string{}
	}
	return rowsResult(cols, rows), nil
}

func influx3Exec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	if strings.EqualFold(cl.params["version"], "2") || strings.EqualFold(cl.params["api"], "v2") {
		return influxExec(ctx, cl, database, q, limit)
	}
	db := database
	if db == "" {
		db = cl.params["database"]
	}
	if db == "" {
		db = "public"
	}
	var root any
	if err := cl.postJSON(ctx, "/api/v3/query_sql", map[string]any{"db": db, "q": q, "format": "json"}, &root); err != nil {
		return nil, err
	}
	if arr, ok := root.([]any); ok {
		cols, rows := rowsFromObjects(arr)
		if len(rows) > limit {
			rows = rows[:limit]
		}
		return rowsResult(cols, rows), nil
	}
	if cols, rows, ok := genericRows(root); ok {
		return rowsResult(cols, rows), nil
	}
	return rawBodyResult(nil), nil
}

func tdengineExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	status, _, raw, err := cl.do(ctx, http.MethodPost, "/rest/sql", nil, q, map[string]string{"Content-Type": "text/plain"})
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, statusError(status, raw)
	}
	var root struct {
		Code       int     `json:"code"`
		Desc       string  `json:"desc"`
		ColumnMeta [][]any `json:"column_meta"`
		Data       [][]any `json:"data"`
		Rows       int     `json:"rows"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("TDengine 响应解析失败: %w", err)
	}
	if root.Code != 0 {
		return nil, fmt.Errorf("TDengine 错误 %d: %s", root.Code, root.Desc)
	}
	cols := make([]string, 0, len(root.ColumnMeta))
	for _, meta := range root.ColumnMeta {
		if len(meta) > 0 {
			cols = append(cols, fmt.Sprint(meta[0]))
		}
	}
	rows := int64(len(root.Data))
	res := &queryResult{Kind: "rows", Columns: cols, Rows: root.Data, Affected: rows}
	if limit > 0 && len(root.Data) > limit {
		res.Rows = root.Data[:limit]
		res.Truncated = true
	}
	return res, nil
}

func iotdbExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var root struct {
		Code        int      `json:"code"`
		Message     string   `json:"message"`
		Expressions []string `json:"expressions"`
		Values      [][]any  `json:"values"`
	}
	if err := cl.postJSON(ctx, "/rest/v1/query", map[string]any{"sql": q}, &root); err != nil {
		return nil, err
	}
	if root.Code != 0 {
		return nil, fmt.Errorf("IoTDB 错误 %d: %s", root.Code, root.Message)
	}
	rows := root.Values
	truncated := false
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	return &queryResult{Kind: "rows", Columns: root.Expressions, Rows: rows, Truncated: truncated}, nil
}

func rqliteExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var root []struct {
		Columns      []string `json:"columns"`
		Values       [][]any  `json:"values"`
		RowsAffected int64    `json:"rows_affected"`
		Error        string   `json:"error"`
	}
	if err := cl.postJSON(ctx, "/db", []string{q}, &root); err != nil {
		return nil, err
	}
	if len(root) == 0 {
		return &queryResult{Kind: "exec"}, nil
	}
	res := root[0]
	if res.Error != "" {
		return nil, fmt.Errorf("rqlite 执行失败: %s", res.Error)
	}
	if res.Columns != nil {
		rows := res.Values
		truncated := false
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
			truncated = true
		}
		return &queryResult{Kind: "rows", Columns: res.Columns, Rows: rows, Truncated: truncated}, nil
	}
	return &queryResult{Kind: "exec", Affected: res.RowsAffected}, nil
}

func tursoExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	body := map[string]any{
		"requests": []any{
			map[string]any{"type": "execute", "stmt": map[string]any{"sql": q, "args": []any{}}},
			map[string]any{"type": "close"},
		},
	}
	var root struct {
		Results []struct {
			Type     string `json:"type"`
			Error    any    `json:"error"`
			Response struct {
				Type   string `json:"type"`
				Result struct {
					Cols []struct {
						Name string `json:"name"`
					} `json:"cols"`
					Rows [][]struct {
						Type  string `json:"type"`
						Value any    `json:"value"`
					} `json:"rows"`
					Affected int64 `json:"affected_row_count"`
				} `json:"result"`
			} `json:"response"`
		} `json:"results"`
	}
	if err := cl.postJSON(ctx, "/v2/pipeline", body, &root); err != nil {
		return nil, err
	}
	if len(root.Results) == 0 {
		return &queryResult{Kind: "exec"}, nil
	}
	first := root.Results[0]
	if first.Type == "error" || first.Error != nil {
		return nil, fmt.Errorf("libSQL 执行失败: %v", first.Error)
	}
	result := first.Response.Result
	cols := make([]string, 0, len(result.Cols))
	for _, c := range result.Cols {
		cols = append(cols, c.Name)
	}
	rows := make([][]any, 0, len(result.Rows))
	for _, r := range result.Rows {
		row := make([]any, len(r))
		for i, cell := range r {
			row[i] = jsonValue(cell.Value)
		}
		if limit > 0 && len(rows) >= limit {
			return &queryResult{Kind: "rows", Columns: cols, Rows: rows, Truncated: true}, nil
		}
		rows = append(rows, row)
	}
	if len(cols) == 0 {
		return &queryResult{Kind: "exec", Affected: result.Affected}, nil
	}
	return rowsResult(cols, rows), nil
}

func d1Exec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var root struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result []struct {
			Success bool             `json:"success"`
			Results []map[string]any `json:"results"`
		} `json:"result"`
	}
	if err := cl.postJSON(ctx, "", map[string]any{"sql": q, "params": []any{}}, &root); err != nil {
		return nil, err
	}
	if len(root.Errors) > 0 {
		return nil, fmt.Errorf("D1 执行失败: %s", root.Errors[0].Message)
	}
	if len(root.Result) == 0 {
		return &queryResult{Kind: "exec"}, nil
	}
	items := make([]any, 0, len(root.Result[0].Results))
	for _, item := range root.Result[0].Results {
		items = append(items, item)
	}
	cols, rows := rowsFromObjects(items)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	if len(cols) == 0 {
		return &queryResult{Kind: "exec"}, nil
	}
	return rowsResult(cols, rows), nil
}

func databricksExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var root map[string]any
	payload := map[string]any{
		"statement":    q,
		"warehouse_id": cl.params["warehouse"],
		"wait_timeout": "50s",
		"format":       "JSON_ARRAY",
	}
	if catalog := cl.params["catalog"]; catalog != "" {
		payload["catalog"] = catalog
	}
	if schema := cl.params["schema"]; schema != "" {
		payload["schema"] = schema
	}
	if err := cl.postJSON(ctx, "/api/2.0/sql/statements", payload, &root); err != nil {
		return nil, err
	}
	state := fmt.Sprint(dig(root, "status", "state"))
	if state != "SUCCEEDED" {
		return nil, fmt.Errorf("Databricks 语句状态: %s", state)
	}
	if cols, rows, ok := databricksRows(root, limit); ok {
		return rowsResult(cols, rows), nil
	}
	return &queryResult{Kind: "exec"}, nil
}

func databricksRows(root map[string]any, limit int) ([]string, [][]any, bool) {
	cols := make([]string, 0, 8)
	if list, ok := dig(root, "manifest", "schema", "columns").([]any); ok {
		for _, item := range list {
			m, _ := item.(map[string]any)
			cols = append(cols, fmt.Sprint(m["name"]))
		}
	}
	arr, ok := dig(root, "result", "data_array").([]any)
	if !ok {
		return nil, nil, false
	}
	rows := make([][]any, 0, len(arr))
	for _, row := range arr {
		if inner, ok := row.([]any); ok {
			if limit > 0 && len(rows) >= limit {
				break
			}
			out := make([]any, len(inner))
			for i, v := range inner {
				out[i] = jsonValue(v)
			}
			rows = append(rows, out)
		}
	}
	return cols, rows, true
}

func bigqueryExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	project := cl.params["project"]
	if project == "" {
		return nil, fmt.Errorf("BigQuery 需要在附加参数中提供 project")
	}
	var root map[string]any
	payload := map[string]any{"query": q, "useLegacySql": false, "maxResults": limit}
	if ds := cl.params["dataset"]; ds != "" {
		payload["defaultDataset"] = map[string]any{"projectId": project, "datasetId": ds}
	}
	if err := cl.postJSON(ctx, "/projects/"+pathEscape(project)+"/queries", payload, &root); err != nil {
		return nil, err
	}
	if complete, ok := root["jobComplete"].(bool); ok && !complete {
		return nil, fmt.Errorf("BigQuery 任务 %v 尚未完成，请稍后重试", root["jobReference"])
	}
	cols := make([]string, 0, 8)
	if schema, ok := root["schema"].(map[string]any); ok {
		if fields, ok := schema["fields"].([]any); ok {
			for _, f := range fields {
				m, _ := f.(map[string]any)
				cols = append(cols, fmt.Sprint(m["name"]))
			}
		}
	}
	rows := make([][]any, 0, 16)
	if rawRows, ok := root["rows"].([]any); ok {
		for _, r := range rawRows {
			if limit > 0 && len(rows) >= limit {
				break
			}
			m, _ := r.(map[string]any)
			fields, _ := m["f"].([]any)
			row := make([]any, len(fields))
			for i, f := range fields {
				fm, _ := f.(map[string]any)
				row[i] = jsonValue(fm["v"])
			}
			rows = append(rows, row)
		}
	}
	return rowsResult(cols, rows), nil
}

func salesforceExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	version := cl.params["apiVersion"]
	if version == "" {
		version = "v60.0"
	}
	var root map[string]any
	query := url.Values{"q": {q}}
	if limit > 0 {
		// SOQL 的 LIMIT 由语句自行控制，这里仅作为结果截断。
	}
	if err := cl.getJSON(ctx, "/services/data/"+version+"/query", query, &root); err != nil {
		return nil, err
	}
	records, _ := root["records"].([]any)
	items := make([]any, 0, len(records))
	for _, r := range records {
		if m, ok := r.(map[string]any); ok {
			delete(m, "attributes")
			items = append(items, m)
		}
	}
	cols, rows := rowsFromObjects(items)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rowsResult(cols, rows), nil
}

func kylinExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	project := cl.params["project"]
	if project == "" {
		project = "learn_kylin"
	}
	var root map[string]any
	if err := cl.postJSON(ctx, "/kylin/api/query", map[string]any{
		"sql": q, "project": project, "limit": limit, "offset": 0,
	}, &root); err != nil {
		return nil, err
	}
	cols := make([]string, 0, 8)
	if metas, ok := root["columnMetas"].([]any); ok {
		for _, item := range metas {
			m, _ := item.(map[string]any)
			cols = append(cols, fmt.Sprint(m["name"]))
		}
	}
	rows := make([][]any, 0, 16)
	if arr, ok := root["results"].([]any); ok {
		for _, item := range arr {
			if inner, ok := item.([]any); ok {
				row := make([]any, len(inner))
				for i, v := range inner {
					row[i] = jsonValue(v)
				}
				rows = append(rows, row)
			}
		}
	}
	return rowsResult(cols, rows), nil
}

func hbaseExec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	path := q
	if !strings.HasPrefix(path, "/") {
		if database != "" {
			path = "/" + pathEscape(database) + "/" + pathEscape(q)
		} else {
			path = "/" + pathEscape(q)
		}
	}
	status, _, raw, err := cl.do(ctx, http.MethodGet, path, nil, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, statusError(status, raw)
	}
	return rawBodyResult(raw), nil
}

func etcdExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var body any
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("请求体必须是合法 JSON: %w", err)
		}
	} else {
		body = map[string]any{
			"key":       base64.StdEncoding.EncodeToString([]byte(q)),
			"range_end": base64.StdEncoding.EncodeToString([]byte{0}),
			"limit":     limit,
		}
	}
	var root map[string]any
	if err := cl.postJSON(ctx, "/v3/kv/range", body, &root); err != nil {
		return nil, err
	}
	kvs, _ := root["kvs"].([]any)
	rows := make([][]any, 0, len(kvs))
	for _, kv := range kvs {
		m, _ := kv.(map[string]any)
		rows = append(rows, []any{decodeBase64String(m["key"]), decodeBase64String(m["value"])})
	}
	return rowsResult([]string{"key", "value"}, rows), nil
}

func decodeBase64String(v any) string {
	s, _ := v.(string)
	if s == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(raw)
}

func consulExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	path := q
	if !strings.HasPrefix(path, "/") {
		path = "/v1/kv/" + strings.TrimPrefix(q, "/")
	}
	status, _, raw, err := cl.do(ctx, http.MethodGet, path, url.Values{"recurse": {"true"}}, nil, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, statusError(status, raw)
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) == nil {
		rows := make([][]any, 0, len(items))
		for _, m := range items {
			rows = append(rows, []any{jsonValue(m["Key"]), decodeBase64String(m["Value"]), jsonValue(m["Flags"])})
		}
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		return rowsResult([]string{"Key", "Value", "Flags"}, rows), nil
	}
	return rawBodyResult(raw), nil
}

func nacosExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	path := q
	query := url.Values{}
	if !strings.HasPrefix(path, "/") {
		path = "/nacos/v1/cs/configs"
		query.Set("search", "accurate")
		query.Set("dataId", q)
		query.Set("pageNo", "1")
		query.Set("pageSize", strconv.Itoa(limit))
	}
	status, _, raw, err := cl.do(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, statusError(status, raw)
	}
	return rawBodyResult(raw), nil
}

func irisExec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	ns := database
	if ns == "" {
		ns = cl.params["namespace"]
	}
	if ns == "" {
		ns = "USER"
	}
	var root map[string]any
	if err := cl.postJSON(ctx, "/api/atelier/v1/"+pathEscape(ns)+"/action/query", map[string]any{
		"query": q, "parameters": []any{},
	}, &root); err != nil {
		return nil, err
	}
	if cols, rows, ok := genericRows(root); ok {
		return rowsResult(cols, rows), nil
	}
	return &queryResult{Kind: "exec"}, nil
}

func igniteExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var root struct {
		Items  [][]any `json:"items"`
		Fields []any   `json:"fields"`
		Error  string  `json:"error"`
	}
	query := url.Values{
		"cmd":      {"qry"},
		"type":     {"SQL_FIELDS"},
		"pageSize": {strconv.Itoa(limit)},
		"sql":      {q},
	}
	if err := cl.getJSON(ctx, "/ignite", query, &root); err != nil {
		return nil, err
	}
	if root.Error != "" {
		return nil, fmt.Errorf("Ignite 执行失败: %s", root.Error)
	}
	if len(root.Items) == 0 {
		return &queryResult{Kind: "exec"}, nil
	}
	cols := make([]string, 0, len(root.Items[0]))
	for _, v := range root.Items[0] {
		cols = append(cols, fmt.Sprint(v))
	}
	rows := root.Items[1:]
	return rowsResult(cols, rows), nil
}

func dynamoExec(ctx context.Context, cl *httpClient, q string, limit int) (*queryResult, error) {
	var body struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(q), &body); err != nil || body.Action == "" {
		return nil, fmt.Errorf(`请求格式应为 {"action":"Scan","params":{"TableName":"..."}}`)
	}
	root, err := dynamoRequest(ctx, cl, body.Action, body.Params)
	if err != nil {
		return nil, err
	}
	if items, ok := root["Items"].([]any); ok {
		cols, rows := rowsFromObjects(items)
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		return rowsResult(cols, rows), nil
	}
	if cols, rows, ok := genericRows(root); ok {
		return rowsResult(cols, rows), nil
	}
	return &queryResult{Kind: "exec"}, nil
}

// dynamoRequest 调用 DynamoDB 的 JSON 协议接口。
func dynamoRequest(ctx context.Context, cl *httpClient, action string, params map[string]any) (map[string]any, error) {
	region := cl.params["region"]
	if region == "" {
		region = "us-east-1"
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cl.fullURL("", nil), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+action)
	awsSigV4(req, payload, cl.params["accessKey"], cl.params["secretKey"], region, "dynamodb")

	resp, err := cl.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, statusError(resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("DynamoDB 响应解析失败: %w", err)
	}
	return out, nil
}

func spannerExec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	project := cl.params["project"]
	instance := cl.params["instance"]
	db := database
	if db == "" {
		db = cl.params["database"]
	}
	if project == "" || instance == "" || db == "" {
		return nil, fmt.Errorf("Spanner 需要在附加参数中提供 project、instance、database")
	}
	var root map[string]any
	path := "/v1/projects/" + pathEscape(project) + "/instances/" + pathEscape(instance) + "/databases/" + pathEscape(db) + ":executeSql"
	if err := cl.postJSON(ctx, path, map[string]any{"sql": q}, &root); err != nil {
		return nil, err
	}
	cols := make([]string, 0, 8)
	if fields, ok := root["metadata"].(map[string]any); ok {
		arr, _ := fields["rowType"].(map[string]any)
		list, _ := arr["fields"].([]any)
		for _, f := range list {
			m, _ := f.(map[string]any)
			cols = append(cols, fmt.Sprint(m["name"]))
		}
	}
	rows := make([][]any, 0, 16)
	if rawRows, ok := root["rows"].([]any); ok {
		for _, r := range rawRows {
			if limit > 0 && len(rows) >= limit {
				break
			}
			arr, _ := r.([]any)
			row := make([]any, len(arr))
			for i, cell := range arr {
				m, _ := cell.(map[string]any)
				for _, key := range []string{"stringValue", "intValue", "boolValue", "floatValue"} {
					if v, ok := m[key]; ok {
						row[i] = v
						break
					}
				}
			}
			rows = append(rows, row)
		}
	}
	return rowsResult(cols, rows), nil
}

func mqExec(ctx context.Context, cl *httpClient, database, q string, limit int) (*queryResult, error) {
	path := q
	if !strings.HasPrefix(path, "/") {
		if database != "" {
			path = "/topics/" + pathEscape(database)
		} else {
			path = "/topics"
		}
	}
	var body any
	if strings.HasPrefix(strings.TrimSpace(q), "{") {
		if err := json.Unmarshal([]byte(q), &body); err != nil {
			return nil, fmt.Errorf("请求体必须是合法 JSON: %w", err)
		}
	}
	status, _, raw, err := cl.do(ctx, http.MethodGet, path, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, statusError(status, raw)
	}
	return rawBodyResult(raw), nil
}

// genericHTTPExec 通用请求：{"method":"GET","path":"/x","body":{},"headers":{}}。
func genericHTTPExec(ctx context.Context, cl *httpClient, q string) (*queryResult, error) {
	var req struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Body    any               `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(q), &req); err != nil {
		return nil, fmt.Errorf(`该类型请在请求面板输入 JSON：{"method":"GET","path":"/api/..."}`)
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	status, _, raw, err := cl.do(ctx, strings.ToUpper(req.Method), req.Path, nil, req.Body, req.Headers)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		return nil, statusError(status, raw)
	}
	return rawBodyResult(raw), nil
}
