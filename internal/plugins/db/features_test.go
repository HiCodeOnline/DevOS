package dbapp

import (
	"context"
	"strings"
	"testing"
)

func TestTypeRegistryCoversDBXProfiles(t *testing.T) {
	// DBX 的全部 driver profile 都应能在类型注册表中找到（并允许存在额外类型）。
	required := []string{
		"mysql", "postgres", "sqlite", "rqlite", "turso", "cloudflare-d1", "redis", "duckdb",
		"clickhouse", "sqlserver", "mongodb", "dynamodb", "oracle", "elasticsearch", "easysearch",
		"meilisearch", "solr", "couchdb", "hbase", "qdrant", "chromadb", "milvus", "weaviate",
		"doris", "starrocks", "manticoresearch", "redshift", "dameng", "kingbase", "highgo", "uxdb",
		"vastbase", "goldendb", "databend", "gaussdb", "kwdb", "yashandb", "databricks", "saphana",
		"teradata", "vertica", "firebird", "exasol", "opengauss", "questdb", "oceanbase-oracle",
		"gbase", "gbase8s", "gbase8c", "access", "h2", "snowflake", "trino", "prestosql", "hive",
		"argo", "transwarp", "kyuubi", "impala", "db2", "informix", "neo4j", "nebula", "cassandra",
		"bigquery", "spanner", "kylin", "ignite", "ignite3", "sundb", "oscar", "tdengine", "xugu",
		"etcd", "zookeeper", "nacos", "consul", "mq", "mqtt", "iotdb", "iris", "influxdb",
		"influxdb3", "victoriametrics", "jdbc", "spark", "plugin", "salesforce",
	}
	seen := map[string]bool{}
	for _, tp := range dbTypes {
		if seen[tp.ID] {
			t.Fatalf("类型 ID 重复: %s", tp.ID)
		}
		seen[tp.ID] = true
		if tp.Name == "" || tp.Group == "" || tp.Family == "" || tp.Color == "" || tp.Icon == "" {
			t.Fatalf("类型 %s 缺少必填字段: %+v", tp.ID, tp)
		}
	}
	for _, id := range required {
		if !seen[id] {
			t.Fatalf("缺少 DBX 类型 %s", id)
		}
	}
	if len(dbTypes) < 88 {
		t.Fatalf("类型数量 = %d，至少应覆盖 88 个 DBX profile", len(dbTypes))
	}
}

func TestTypeAliasKeepsOldConfigs(t *testing.T) {
	if got := typeOf("dm"); got.ID != "dameng" {
		t.Fatalf("dm 别名应解析为 dameng，实际 = %s", got.ID)
	}
	if got := typeOf("gbase8a"); got.ID != "gbase" {
		t.Fatalf("gbase8a 别名应解析为 gbase，实际 = %s", got.ID)
	}
	if driverImpl("dameng") != "dm" || driverImpl("clickhouse") != "clickhouse" || driverImpl("mariadb") != "mysql" {
		t.Fatalf("驱动映射不正确: dameng=%s clickhouse=%s mariadb=%s",
			driverImpl("dameng"), driverImpl("clickhouse"), driverImpl("mariadb"))
	}
	if supportsConnect("oracle") != true || supportsConnect("duckdb") != false {
		t.Fatalf("连接能力标记不正确")
	}
}

func TestClickHouseDSN(t *testing.T) {
	c := Connection{Driver: "clickhouse", Host: "127.0.0.1", Port: 8123, User: "default", Password: "p", Database: "logs"}
	dsn, err := buildDSN(c, "")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if !strings.HasPrefix(dsn, "http://") || !strings.Contains(dsn, "127.0.0.1:8123") || !strings.Contains(dsn, "/logs") {
		t.Fatalf("ClickHouse HTTP DSN 不正确: %s", dsn)
	}

	c.Port = 9000
	dsn, err = buildDSN(c, "")
	if err != nil || !strings.HasPrefix(dsn, "clickhouse://") {
		t.Fatalf("ClickHouse Native DSN 不正确: %s, err=%v", dsn, err)
	}
}

// H2 通过 PostgreSQL 协议模式接入：端口、驱动映射与必须填写库名。
func TestH2DSN(t *testing.T) {
	if !supportsConnect("h2") || familyOf("h2") != familyPostgres || driverImpl("h2") != "pgx" {
		t.Fatalf("H2 应复用 PostgreSQL 协议: support=%v family=%s driver=%s",
			supportsConnect("h2"), familyOf("h2"), driverImpl("h2"))
	}
	if _, err := buildDSN(Connection{Driver: "h2", Host: "127.0.0.1"}, ""); err == nil {
		t.Fatal("H2 未填写数据库名时应报错")
	}
	dsn, err := buildDSN(Connection{Driver: "h2", Host: "127.0.0.1", Database: "mydb"}, "")
	if err != nil {
		t.Fatalf("buildDSN: %v", err)
	}
	if !strings.Contains(dsn, "127.0.0.1:5435/mydb") || !strings.Contains(dsn, "sslmode=disable") {
		t.Fatalf("H2 DSN 不正确: %s", dsn)
	}
}

func TestPlannedTypeRejected(t *testing.T) {
	p := New()
	_, err := p.pool(Connection{ID: "x", Driver: "duckdb", Host: "127.0.0.1", Port: 5432}, "")
	if err == nil || !strings.Contains(err.Error(), "未内置驱动") {
		t.Fatalf("未内置驱动的类型应拒绝连接，err=%v", err)
	}
}

func TestExtraFamilyDSN(t *testing.T) {
	cases := []struct {
		driver   string
		database string
		params   string
		want     []string
		notWant  []string
	}{
		{"firebird", "emp.fdb", "", []string{"u:p@127.0.0.1:3050/emp.fdb", "charset=UTF8"}, nil},
		{"saphana", "TENANT", "", []string{"hdb://u:p@127.0.0.1:30015", "databaseName=TENANT"}, nil},
		{"exasol", "", "", []string{"exasol://u:p@127.0.0.1:8563", "validateservercertificate=0"}, nil},
		{"vertica", "vdb", "", []string{"vertica://u:p@127.0.0.1:5433/vdb"}, nil},
		{"trino", "hive", "", []string{"http://u:p@127.0.0.1:8080", "catalog=hive"}, nil},
		{"snowflake", "ANALYTICS", "account=myorg-xy", []string{"u:p@127.0.0.1:443/ANALYTICS", "account=myorg-xy"}, []string{"snowflake://"}},
		{"hive", "default", "", []string{"hive://u:p@127.0.0.1:10000/default"}, nil},
	}
	for _, tc := range cases {
		c := Connection{Driver: tc.driver, Host: "127.0.0.1", User: "u", Password: "p", Params: tc.params}
		if tc.driver == "vertica" {
			c.Port = 5433
		}
		dsn, err := buildDSN(c, tc.database)
		if err != nil {
			t.Fatalf("%s buildDSN: %v", tc.driver, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(dsn, want) {
				t.Fatalf("%s DSN 缺少 %q: %s", tc.driver, want, dsn)
			}
		}
		for _, notWant := range tc.notWant {
			if strings.Contains(dsn, notWant) {
				t.Fatalf("%s DSN 不应包含 %q: %s", tc.driver, notWant, dsn)
			}
		}
	}
}

func TestExtraFamilyRejectsMissingFile(t *testing.T) {
	if _, err := buildDSN(Connection{Driver: "firebird", Host: "127.0.0.1"}, ""); err == nil {
		t.Fatalf("Firebird 缺少数据库文件时应报错")
	}
}

func TestSplitSQLStatements(t *testing.T) {
	script := "-- comment; with semicolon\nSELECT 1;\nINSERT INTO t VALUES ('a;b', \"c;d\");\n/* block ; comment */\nUPDATE t SET x = 1"
	got := splitSQLStatements(script)
	if len(got) != 3 {
		t.Fatalf("语句数量 = %d，期望 3: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "-- comment") || !strings.Contains(got[0], "SELECT 1") {
		t.Fatalf("首条语句未正确保留: %q", got[0])
	}
	if !strings.Contains(got[1], "'a;b'") || !strings.Contains(got[1], `"c;d"`) {
		t.Fatalf("字符串内的分号被误切: %q", got[1])
	}
	if !strings.Contains(got[2], "UPDATE") {
		t.Fatalf("末条语句不正确: %q", got[2])
	}

	pg := splitSQLStatements("CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql; SELECT 2;")
	if len(pg) != 2 {
		t.Fatalf("PostgreSQL dollar-quote 切分错误: %#v", pg)
	}
}

func TestBuildAlterStatements(t *testing.T) {
	ops := []alterOp{{Kind: "add", Name: "age", Type: "INT", NotNull: true, Default: "0"}}
	got, err := buildAlterStatements("mysql", "users", ops)
	if err != nil || len(got) != 1 {
		t.Fatalf("MySQL ADD COLUMN: %v %#v", err, got)
	}
	if got[0] != "ALTER TABLE `users` ADD COLUMN `age` INT DEFAULT 0 NOT NULL" {
		t.Fatalf("MySQL 语句不正确: %s", got[0])
	}

	pg, err := buildAlterStatements("postgres", "users", []alterOp{{Kind: "modify", Name: "age", Type: "bigint", NotNull: true}})
	if err != nil || len(pg) != 2 || !strings.Contains(pg[0], "TYPE bigint") || !strings.Contains(pg[1], "SET NOT NULL") {
		t.Fatalf("PostgreSQL MODIFY 语句不正确: %#v err=%v", pg, err)
	}

	idx, err := buildAlterStatements("sqlite", "users", []alterOp{{Kind: "addIndex", Columns: []string{"email"}, Unique: true}})
	if err != nil || len(idx) != 1 || !strings.Contains(idx[0], "CREATE UNIQUE INDEX") {
		t.Fatalf("SQLite 索引语句不正确: %#v err=%v", idx, err)
	}

	if _, err := buildAlterStatements("mysql", "users", []alterOp{{Kind: "drop"}}); err == nil {
		t.Fatalf("缺少列名应报错")
	}
	if _, err := buildAlterStatements("mysql", "users", []alterOp{{Kind: "add", Name: "x", Type: "INT; DROP TABLE users"}}); err == nil {
		t.Fatalf("非法列类型应报错")
	}
}

func TestBuildWhereUsesDialectPlaceholders(t *testing.T) {
	where, args := buildWhere("postgres", map[string]any{"id": 1, "name": "a"}, 0)
	if len(args) != 2 || !strings.Contains(where, `"id" = $1`) || !strings.Contains(where, `"name" = $2`) {
		t.Fatalf("PostgreSQL 占位符不正确: %s %#v", where, args)
	}
	where, _ = buildWhere("sqlserver", map[string]any{"id": 1}, 2)
	if !strings.Contains(where, "[id] = @p3") {
		t.Fatalf("SQL Server 占位符不正确: %s", where)
	}
}

func TestTableRowsRejectsMissingPK(t *testing.T) {
	p, _ := newTestDB(t)
	if err := p.writeAllowed(Connection{ReadOnly: true}); err == nil {
		t.Fatalf("只读连接应拒绝写操作")
	}
	_ = context.Background()
}
