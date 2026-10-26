package dbapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SupportHTTP 表示通过 HTTP/REST 协议接入的类型。
const SupportHTTP = "http"

// queryResult 描述一次查询/请求的执行结果，字段与 /query 的响应保持一致。
type queryResult struct {
	Kind      string
	Columns   []string
	Rows      [][]any
	Affected  int64
	Truncated bool
}

// httpClient 封装某一连接的 HTTP 基础信息与鉴权方式。
type httpClient struct {
	base       string
	user       string
	pass       string
	token      string
	kind       string // none | basic | bearer | header | aws
	headerName string
	params     map[string]string
	hc         *http.Client
}

// newHTTPClient 依据连接配置构造 HTTP 客户端。
// 附加参数支持：url、scheme、basePath、token、auth、apikey、apikeyheader、insecure、timeout。
func newHTTPClient(c Connection) (*httpClient, error) {
	params := parseParams(c.Params)
	base := strings.TrimSpace(params["url"])
	if base == "" {
		host := strings.TrimSpace(c.Host)
		if host == "" {
			return nil, fmt.Errorf("主机地址不能为空")
		}
		if strings.Contains(host, "://") {
			base = host
		} else {
			scheme := strings.ToLower(strings.TrimSpace(params["scheme"]))
			port := defPort(c)
			if scheme == "" {
				if port == 443 || port == 8443 {
					scheme = "https"
				} else {
					scheme = "http"
				}
			}
			authority := host
			// 主机里已显式带端口时不再重复拼接。
			if _, _, err := net.SplitHostPort(host); err != nil {
				if !(scheme == "https" && port == 443) && !(scheme == "http" && port == 80) {
					authority = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
				}
			}
			base = scheme + "://" + authority
		}
	}
	base = strings.TrimRight(base, "/")
	if p := strings.TrimSpace(params["basePath"]); p != "" {
		base += "/" + strings.Trim(p, "/")
	}

	kind := strings.ToLower(strings.TrimSpace(params["auth"]))
	token := strings.TrimSpace(params["token"])
	if token == "" && (kind == "bearer" || kind == "header") {
		token = c.Password
	}
	switch {
	case kind == "" && strings.TrimSpace(params["apikey"]) != "":
		kind = "header"
		if token == "" {
			token = strings.TrimSpace(params["apikey"])
		}
	case kind == "" && token != "":
		kind = "bearer"
	case kind == "":
		kind = "basic"
	}

	timeout := 20 * time.Second
	if raw := strings.TrimSpace(params["timeout"]); raw != "" {
		if d, err := time.ParseDuration(raw + "s"); err == nil && d > 0 {
			timeout = d
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.EqualFold(params["insecure"], "true") {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &httpClient{
		base:       base,
		user:       c.User,
		pass:       c.Password,
		token:      token,
		kind:       kind,
		headerName: strings.TrimSpace(params["apikeyheader"]),
		params:     params,
		hc:         &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// fullURL 拼接请求地址，path 以 http 开头时直接使用。
func (cl *httpClient) fullURL(path string, query url.Values) string {
	if path == "" {
		if len(query) > 0 {
			return cl.base + "?" + query.Encode()
		}
		return cl.base
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		if len(query) > 0 {
			return path + "?" + query.Encode()
		}
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	out := cl.base + path
	if len(query) > 0 {
		out += "?" + query.Encode()
	}
	return out
}

// do 执行一次 HTTP 请求，返回状态码与响应体（最大 16MB）。
func (cl *httpClient) do(ctx context.Context, method, path string, query url.Values, body any, header map[string]string) (int, http.Header, []byte, error) {
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(v)
	case string:
		reader = strings.NewReader(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("请求体编码失败: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, cl.fullURL(path, query), reader)
	if err != nil {
		return 0, nil, nil, err
	}
	switch cl.kind {
	case "bearer":
		if cl.token != "" {
			req.Header.Set("Authorization", "Bearer "+cl.token)
		}
	case "header":
		if cl.headerName != "" && cl.token != "" {
			req.Header.Set(cl.headerName, cl.token)
		}
	case "basic":
		if cl.user != "" {
			req.SetBasicAuth(cl.user, cl.pass)
		}
	}
	if body != nil {
		if _, ok := body.([]byte); !ok {
			if _, ok := body.(string); !ok {
				req.Header.Set("Content-Type", "application/json")
			}
		}
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := cl.hc.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, raw, nil
}

// statusError 把非 2xx 响应整理成可读错误。
func statusError(status int, raw []byte) error {
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		return fmt.Errorf("HTTP %d", status)
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return fmt.Errorf("HTTP %d: %s", status, msg)
}

func (cl *httpClient) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	status, _, raw, err := cl.do(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return statusError(status, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("响应解析失败: %w", err)
	}
	return nil
}

func (cl *httpClient) postJSON(ctx context.Context, path string, body any, out any) error {
	status, _, raw, err := cl.do(ctx, http.MethodPost, path, nil, body, nil)
	if err != nil {
		return err
	}
	if status >= 300 {
		return statusError(status, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("响应解析失败: %w", err)
	}
	return nil
}

// httpAdapterFor 返回类型对应的适配器标识，未实现专用适配的类型使用通用 HTTP 适配。
func httpAdapterID(driver string) string { return typeOf(driver).ID }

// httpPing 通过适配器探测连接并返回版本信息。
func (p *DB) httpPing(c Connection) (int64, string, error) {
	cl, err := newHTTPClient(c)
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	start := time.Now()
	version, err := httpProbe(ctx, cl, httpAdapterID(c.Driver))
	if err != nil {
		return 0, "", err
	}
	return time.Since(start).Milliseconds(), version, nil
}

func (p *DB) httpDatabases(c Connection) ([]string, error) {
	cl, err := newHTTPClient(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return httpListDatabases(ctx, cl, httpAdapterID(c.Driver))
}

func (p *DB) httpObjects(c Connection, database string) ([]Object, error) {
	cl, err := newHTTPClient(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return httpListObjects(ctx, cl, httpAdapterID(c.Driver), database)
}

// httpQuery 执行适配器查询；max <= 0 时使用默认行数上限。
func (p *DB) httpQuery(c Connection, database, q string, max int) (*queryResult, error) {
	cl, err := newHTTPClient(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return httpExec(ctx, cl, httpAdapterID(c.Driver), database, strings.TrimSpace(q), max)
}

// httpRequestIsWrite 判断 HTTP 请求文本是否包含写操作，用于只读连接拦截。
func httpRequestIsWrite(q string) bool {
	trimmed := strings.TrimSpace(q)
	if strings.HasPrefix(trimmed, "{") {
		var body struct {
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(trimmed), &body) == nil && body.Method != "" {
			m := strings.ToUpper(body.Method)
			return m != http.MethodGet && m != http.MethodHead
		}
	}
	return isWriteSQL(q)
}

/* ---------- JSON 结果整形 ---------- */

// jsonValue 把任意值转换成可 JSON 序列化的单元格值。
func jsonValue(v any) any {
	switch t := v.(type) {
	case nil, string, bool, float64, int, int64:
		return v
	case json.Number:
		return t.String()
	case map[string]any, []any:
		raw, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(raw)
	default:
		return fmt.Sprint(t)
	}
}

// itemColumns 返回对象的列名（按字母序保证输出稳定）。
func itemColumns(m map[string]any) []string {
	cols := make([]string, 0, len(m))
	for k := range m {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

// rowsFromObjects 将对象数组转换为列/行；非对象元素使用 value 单列。
func rowsFromObjects(items []any) ([]string, [][]any) {
	cols := make([]string, 0, 8)
	seen := map[string]int{}
	var rows [][]any
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			m = map[string]any{"value": item}
		}
		keys := itemColumns(m)
		index := make(map[string]int, len(keys))
		for _, k := range keys {
			if pos, ok := seen[k]; ok {
				index[k] = pos
				continue
			}
			seen[k] = len(cols)
			index[k] = len(cols)
			cols = append(cols, k)
		}
		row := make([]any, len(cols))
		for k, v := range m {
			row[index[k]] = jsonValue(v)
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// firstArray 从响应中取出候选数组，适配常见的嵌套结构。
func firstArray(root any, paths ...[]string) []any {
	for _, path := range paths {
		cur := root
		ok := true
		for _, seg := range path {
			m, isMap := cur.(map[string]any)
			if !isMap {
				ok = false
				break
			}
			cur, isMap = m[seg]
			if !isMap {
				ok = false
				break
			}
		}
		if ok {
			if arr, isArr := cur.([]any); isArr {
				return arr
			}
		}
	}
	return nil
}

// dig 按路径读取嵌套 JSON 值。
func dig(root any, path ...string) any {
	cur := root
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	return cur
}

// stringList 把字符串数组或对象数组中的指定字段整理成列表。
func stringList(v any, keys ...string) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	seen := map[string]bool{}
	for _, item := range arr {
		name := ""
		switch t := item.(type) {
		case string:
			name = t
		case map[string]any:
			for _, k := range keys {
				if s, ok := t[k].(string); ok && s != "" {
					name = s
					break
				}
			}
			if name == "" {
				// 形如 {"name": {"name": "x"}} 的对象再尝试一次。
				for _, k := range keys {
					if inner, ok := t[k].(map[string]any); ok {
						if s, ok := inner["name"].(string); ok && s != "" {
							name = s
							break
						}
					}
				}
			}
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// arrayRows 把「列名 + 二维数组」结构转换为结果集。
func arrayRows(cols []string, raw []any, max int) ([]string, [][]any, bool) {
	rows := make([][]any, 0, len(raw))
	for _, item := range raw {
		if max > 0 && len(rows) >= max {
			return cols, rows, true
		}
		arr, ok := item.([]any)
		if !ok {
			continue
		}
		row := make([]any, len(cols))
		for i := range cols {
			if i < len(arr) {
				row[i] = jsonValue(arr[i])
			}
		}
		rows = append(rows, row)
	}
	return cols, rows, false
}

// awsSigV4 为请求附加 AWS Signature V4 签名（DynamoDB 等）。
func awsSigV4(req *http.Request, body []byte, accessKey, secretKey, region, service string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	for _, k := range []string{"content-type", "x-amz-target"} {
		if req.Header.Get(k) != "" {
			headers = append(headers, k)
		}
	}
	sort.Strings(headers)
	var canonicalHeaders strings.Builder
	for _, k := range headers {
		v := req.Header.Get(k)
		if k == "host" {
			v = req.URL.Host
		}
		canonicalHeaders.WriteString(k + ":" + strings.TrimSpace(v) + "\n")
	}
	signedHeaders := strings.Join(headers, ";")

	query := req.URL.Query()
	canonicalQuery := query.Encode()
	if canonicalQuery != "" {
		canonicalQuery = strings.ReplaceAll(canonicalQuery, "+", "%20")
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	hmacSign := func(key []byte, data string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(data))
		return h.Sum(nil)
	}
	kDate := hmacSign([]byte("AWS4"+secretKey), date)
	kRegion := hmacSign(kDate, region)
	kService := hmacSign(kRegion, service)
	kSigning := hmacSign(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSign(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature,
	))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// basicAuthValue 返回 Base64 编码的 basic 凭据。
func basicAuthValue(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
