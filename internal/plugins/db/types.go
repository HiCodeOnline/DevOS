package dbapp

// 数据库协议族：不同族在 DSN、分页、标识符转义与元数据查询上存在差异。
const (
	familyMySQL      = "mysql"
	familyPostgres   = "postgres"
	familySQLite     = "sqlite"
	familyOracle     = "oracle"
	familySQLServer  = "sqlserver"
	familyRedis      = "redis"
	familyMongo      = "mongo"
	familyClickHouse = "clickhouse"
	familyHTTP       = "http" // 预留：需要专用驱动/HTTP 客户端的类型
)

// 类型支持级别。
const (
	// SupportNative 表示内置原生驱动，可直接连接与浏览。
	SupportNative = "native"
	// SupportProtocol 表示复用兼容协议（如 MySQL / PostgreSQL 协议）连接。
	SupportProtocol = "protocol"
	// SupportPlanned 表示当前版本尚未内置驱动，仅可在类型列表中选用与保存配置。
	SupportPlanned = "planned"
)

// 界面分组名，前端按名称映射到三语文案。
const (
	groupCommon     = "常用"
	groupRelational = "关系型"
	groupDomestic   = "国产化"
	groupCloud      = "云与分布式"
	groupNoSQL      = "NoSQL"
	groupSearch     = "搜索与向量"
	groupBigData    = "大数据"
	groupTimeSeries = "时序与流"
	groupInfra      = "目录与服务"
	groupOther      = "其他"
)

// DBType 描述一种可连接的数据库类型。
type DBType struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Family       string   `json:"family"`
	Group        string   `json:"group"`
	DefaultPort  int      `json:"defaultPort,omitempty"`
	SchemaLevel  bool     `json:"schemaLevel,omitempty"` // 库下是否还有模式层级
	File         bool     `json:"file,omitempty"`        // 是否使用本地数据库文件
	SQL          bool     `json:"sql"`                   // 是否使用 SQL 工作区
	Color        string   `json:"color"`
	Icon         string   `json:"icon"`    // 前端内置图标键
	Support      string   `json:"support"` // native | protocol | planned
	Capabilities []string `json:"capabilities"`
	DatabaseLabel string  `json:"databaseLabel,omitempty"`
	Hint          string  `json:"hint,omitempty"`
}

func nt(id, name, family, group string, port int, color, icon, support string) DBType {
	return DBType{ID: id, Name: name, Family: family, Group: group, DefaultPort: port, Color: color, Icon: icon, Support: support}
}

// dbTypes 按展示顺序分组列出支持的数据库类型（对齐 DBX 的 88 个 driver profile）。
var dbTypes = func() []DBType {
	list := []DBType{
		// 常用
		nt("mysql", "MySQL", familyMySQL, groupCommon, 3306, "#00758f", "mysql", SupportNative),
		nt("postgres", "PostgreSQL", familyPostgres, groupCommon, 5432, "#336791", "postgres", SupportNative),
		nt("sqlite", "SQLite", familySQLite, groupCommon, 0, "#10b981", "sqlite", SupportNative),
		nt("sqlserver", "SQL Server", familySQLServer, groupCommon, 1433, "#cc2927", "sqlserver", SupportNative),
		nt("oracle", "Oracle", familyOracle, groupCommon, 1521, "#c74634", "oracle", SupportNative),
		nt("redis", "Redis", familyRedis, groupCommon, 6379, "#dc382d", "redis", SupportNative),
		nt("mongodb", "MongoDB", familyMongo, groupCommon, 27017, "#10aa50", "mongo", SupportNative),
		nt("clickhouse", "ClickHouse", familyClickHouse, groupCommon, 8123, "#eab308", "clickhouse", SupportNative),
		nt("mariadb", "MariaDB", familyMySQL, groupCommon, 3306, "#00758f", "mysql", SupportProtocol),

		// 关系型
		nt("duckdb", "DuckDB", familyHTTP, groupRelational, 0, "#f59e0b", "analytics", SupportPlanned),
		nt("rqlite", "RQLite", familyHTTP, groupRelational, 4001, "#0ea5e9", "database", SupportPlanned),
		nt("turso", "Turso", familyHTTP, groupRelational, 443, "#16a34a", "cloud", SupportPlanned),
		nt("cloudflare-d1", "Cloudflare D1", familyHTTP, groupRelational, 443, "#f38020", "cloud", SupportPlanned),
		nt("firebird", "Firebird", familyHTTP, groupRelational, 3050, "#f43f5e", "database", SupportPlanned),
		nt("exasol", "Exasol", familyHTTP, groupRelational, 8563, "#1e40af", "analytics", SupportPlanned),
		nt("vertica", "Vertica", familyPostgres, groupRelational, 5433, "#2563eb", "analytics", SupportProtocol),
		nt("h2", "H2", familyHTTP, groupRelational, 9092, "#2563eb", "database", SupportPlanned),
		nt("db2", "IBM DB2", familyHTTP, groupRelational, 50000, "#1d4ed8", "database", SupportPlanned),
		nt("informix", "IBM Informix", familyHTTP, groupRelational, 9088, "#0f766e", "database", SupportPlanned),
		nt("access", "Microsoft Access", familyHTTP, groupRelational, 0, "#a21caf", "file", SupportPlanned),
		nt("saphana", "SAP HANA", familyHTTP, groupRelational, 30015, "#0ea5e9", "analytics", SupportPlanned),
		nt("teradata", "Teradata", familyHTTP, groupRelational, 1025, "#e11d48", "analytics", SupportPlanned),

		// 国产化
		nt("dameng", "达梦 DM8", familyOracle, groupDomestic, 5236, "#0f766e", "oracle", SupportNative),
		nt("kingbase", "金仓 KingbaseES", familyPostgres, groupDomestic, 54321, "#b91c1c", "postgres", SupportProtocol),
		nt("highgo", "瀚高 HighGo", familyPostgres, groupDomestic, 5866, "#dc2626", "postgres", SupportProtocol),
		nt("uxdb", "优炫 UXDB", familyPostgres, groupDomestic, 52025, "#0891b2", "postgres", SupportProtocol),
		nt("vastbase", "海量 Vastbase", familyPostgres, groupDomestic, 5432, "#7c3aed", "postgres", SupportProtocol),
		nt("gaussdb", "华为 GaussDB", familyPostgres, groupDomestic, 5432, "#e11d48", "postgres", SupportProtocol),
		nt("opengauss", "openGauss", familyPostgres, groupDomestic, 5432, "#e11d48", "postgres", SupportProtocol),
		nt("gbase", "南大通用 GBase 8a", familyMySQL, groupDomestic, 5258, "#ea580c", "mysql", SupportProtocol),
		nt("gbase8c", "南大通用 GBase 8c", familyPostgres, groupDomestic, 5432, "#ea580c", "postgres", SupportProtocol),
		nt("gbase8s", "南大通用 GBase 8s", familyHTTP, groupDomestic, 9088, "#9a3412", "database", SupportPlanned),
		nt("goldendb", "金篆 GoldenDB", familyMySQL, groupDomestic, 3306, "#b45309", "mysql", SupportProtocol),
		nt("kwdb", "KWDB", familyPostgres, groupDomestic, 26257, "#0d9488", "postgres", SupportProtocol),
		nt("oceanbase-oracle", "OceanBase Oracle 模式", familyOracle, groupDomestic, 2883, "#1e40af", "oracle", SupportProtocol),
		nt("yashandb", "崖山 YashanDB", familyOracle, groupDomestic, 1688, "#0e7490", "oracle", SupportProtocol),
		nt("oscar", "神通 OSCAR", familyOracle, groupDomestic, 2003, "#be123c", "oracle", SupportProtocol),
		nt("xugu", "虚谷 XuguDB", familyOracle, groupDomestic, 5138, "#b45309", "oracle", SupportProtocol),
		nt("sundb", "科蓝 SUNDB", familyHTTP, groupDomestic, 22581, "#9333ea", "database", SupportPlanned),

		// 云与分布式
		nt("redshift", "Amazon Redshift", familyPostgres, groupCloud, 5439, "#8b5cf6", "analytics", SupportProtocol),
		nt("databend", "Databend", familyMySQL, groupCloud, 8000, "#7c3aed", "analytics", SupportProtocol),
		nt("databricks", "Databricks SQL", familyHTTP, groupCloud, 443, "#ff3621", "cloud", SupportPlanned),
		nt("snowflake", "Snowflake", familyHTTP, groupCloud, 443, "#29b5e8", "cloud", SupportPlanned),
		nt("bigquery", "Google BigQuery", familyHTTP, groupCloud, 0, "#4285f4", "cloud", SupportPlanned),
		nt("spanner", "Google Cloud Spanner", familyHTTP, groupCloud, 443, "#4285f4", "cloud", SupportPlanned),
		nt("dynamodb", "Amazon DynamoDB", familyHTTP, groupCloud, 8000, "#4053d6", "cloud", SupportPlanned),
		nt("salesforce", "Salesforce", familyHTTP, groupCloud, 443, "#00a1e0", "cloud", SupportPlanned),
		nt("trino", "Trino", familyHTTP, groupCloud, 8080, "#dd00a1", "analytics", SupportPlanned),
		nt("prestosql", "PrestoSQL", familyHTTP, groupCloud, 8080, "#dd00a1", "analytics", SupportPlanned),
		nt("kylin", "Apache Kylin", familyHTTP, groupCloud, 7070, "#6366f1", "analytics", SupportPlanned),
		nt("tidb", "TiDB", familyMySQL, groupCloud, 4000, "#dc2626", "mysql", SupportProtocol),
		nt("oceanbase", "OceanBase MySQL 模式", familyMySQL, groupCloud, 2881, "#1e40af", "mysql", SupportProtocol),
		nt("polardb_mysql", "PolarDB MySQL", familyMySQL, groupCloud, 3306, "#f97316", "mysql", SupportProtocol),
		nt("polardb_pg", "PolarDB PostgreSQL", familyPostgres, groupCloud, 5432, "#f97316", "postgres", SupportProtocol),
		nt("tdsql_mysql", "TDSQL MySQL", familyMySQL, groupCloud, 3306, "#2563eb", "mysql", SupportProtocol),

		// NoSQL
		nt("couchdb", "Apache CouchDB", familyHTTP, groupNoSQL, 5984, "#e42528", "document", SupportPlanned),
		nt("cassandra", "Apache Cassandra", familyHTTP, groupNoSQL, 9042, "#1287b1", "widecolumn", SupportPlanned),
		nt("hbase", "Apache HBase", familyHTTP, groupNoSQL, 8080, "#f97316", "widecolumn", SupportPlanned),
		nt("ignite", "Apache Ignite", familyHTTP, groupNoSQL, 10800, "#ec4899", "widecolumn", SupportPlanned),
		nt("ignite3", "Apache Ignite 3", familyHTTP, groupNoSQL, 10800, "#db2777", "widecolumn", SupportPlanned),
		nt("neo4j", "Neo4j", familyHTTP, groupNoSQL, 7687, "#018bff", "graph", SupportPlanned),
		nt("nebula", "NebulaGraph", familyHTTP, groupNoSQL, 9669, "#7c3aed", "graph", SupportPlanned),

		// 搜索与向量
		nt("elasticsearch", "Elasticsearch", familyHTTP, groupSearch, 9200, "#f59e0b", "search", SupportPlanned),
		nt("easysearch", "Easysearch", familyHTTP, groupSearch, 9200, "#10b981", "search", SupportPlanned),
		nt("meilisearch", "Meilisearch", familyHTTP, groupSearch, 7700, "#f472b6", "search", SupportPlanned),
		nt("solr", "Apache Solr", familyHTTP, groupSearch, 8983, "#d9411e", "search", SupportPlanned),
		nt("manticoresearch", "Manticore Search", familyMySQL, groupSearch, 9306, "#e11d48", "search", SupportProtocol),
		nt("qdrant", "Qdrant", familyHTTP, groupSearch, 6333, "#dc244c", "vector", SupportPlanned),
		nt("chromadb", "ChromaDB", familyHTTP, groupSearch, 8000, "#f97316", "vector", SupportPlanned),
		nt("milvus", "Milvus", familyHTTP, groupSearch, 19530, "#00a1ea", "vector", SupportPlanned),
		nt("weaviate", "Weaviate", familyHTTP, groupSearch, 8080, "#06b6d4", "vector", SupportPlanned),

		// 大数据
		nt("doris", "Apache Doris", familyMySQL, groupBigData, 9030, "#1d4ed8", "analytics", SupportProtocol),
		nt("starrocks", "StarRocks", familyMySQL, groupBigData, 9030, "#2563eb", "analytics", SupportProtocol),
		nt("hive", "Apache Hive", familyHTTP, groupBigData, 10000, "#eab308", "analytics", SupportPlanned),
		nt("impala", "Apache Impala", familyHTTP, groupBigData, 21050, "#f97316", "analytics", SupportPlanned),
		nt("spark", "Apache Spark", familyHTTP, groupBigData, 10015, "#e25a1c", "analytics", SupportPlanned),
		nt("kyuubi", "Apache Kyuubi", familyHTTP, groupBigData, 10009, "#f97316", "analytics", SupportPlanned),
		nt("argo", "星环 Argo", familyHTTP, groupBigData, 10000, "#0ea5e9", "analytics", SupportPlanned),
		nt("transwarp", "星环 Inceptor", familyHTTP, groupBigData, 10000, "#dc2626", "analytics", SupportPlanned),

		// 时序与流
		nt("questdb", "QuestDB", familyPostgres, groupTimeSeries, 8812, "#d97706", "timeseries", SupportProtocol),
		nt("tdengine", "TDengine", familyHTTP, groupTimeSeries, 6041, "#0ea5e9", "timeseries", SupportPlanned),
		nt("iotdb", "Apache IoTDB", familyHTTP, groupTimeSeries, 6667, "#0ea5e9", "timeseries", SupportPlanned),
		nt("influxdb", "InfluxDB", familyHTTP, groupTimeSeries, 8086, "#22adf6", "timeseries", SupportPlanned),
		nt("influxdb3", "InfluxDB 3.x", familyHTTP, groupTimeSeries, 8181, "#0ea5e9", "timeseries", SupportPlanned),
		nt("victoriametrics", "VictoriaMetrics", familyHTTP, groupTimeSeries, 8428, "#e11d48", "timeseries", SupportPlanned),
		nt("mq", "Message Queue", familyHTTP, groupTimeSeries, 8080, "#f59e0b", "message", SupportPlanned),
		nt("mqtt", "MQTT", familyHTTP, groupTimeSeries, 1883, "#9333ea", "message", SupportPlanned),

		// 目录与服务
		nt("etcd", "etcd", familyHTTP, groupInfra, 2379, "#419eda", "keyvalue", SupportPlanned),
		nt("zookeeper", "Apache ZooKeeper", familyHTTP, groupInfra, 2181, "#f59e0b", "directory", SupportPlanned),
		nt("nacos", "Nacos", familyHTTP, groupInfra, 8848, "#22d3ee", "directory", SupportPlanned),
		nt("consul", "Consul", familyHTTP, groupInfra, 8500, "#e03875", "directory", SupportPlanned),

		// 其他
		nt("iris", "InterSystems IRIS", familyHTTP, groupOther, 1972, "#6366f1", "database", SupportPlanned),
		nt("jdbc", "JDBC 通用", familyHTTP, groupOther, 0, "#64748b", "file", SupportPlanned),
		nt("plugin", "插件驱动", familyHTTP, groupOther, 0, "#64748b", "generic", SupportPlanned),
	}
	for i := range list {
		t := &list[i]
		t.SchemaLevel = schemaLevelFor(t.Family)
		t.File = fileSource(t.ID)
		t.SQL = sqlFamily(t.Family)
		t.Capabilities = capsFor(*t)
		if t.DatabaseLabel == "" {
			t.DatabaseLabel = databaseLabelFor(*t)
		}
	}
	return list
}()

// dbTypeAlias 兼容早期版本保存的连接类型 ID。
var dbTypeAlias = map[string]string{
	"dm":      "dameng",
	"gbase8a": "gbase",
}

var dbTypeIndex = func() map[string]DBType {
	out := make(map[string]DBType, len(dbTypes))
	for _, t := range dbTypes {
		out[t.ID] = t
	}
	return out
}()

// typeOf 查找类型定义，未知类型回退为 MySQL。
func typeOf(id string) DBType {
	if alias, ok := dbTypeAlias[id]; ok {
		id = alias
	}
	if t, ok := dbTypeIndex[id]; ok {
		return t
	}
	return dbTypes[0]
}

// familyOf 返回连接类型所属的协议族。
func familyOf(id string) string { return typeOf(id).Family }

// supportsConnect 表示该类型是否内置了可用的连接驱动。
func supportsConnect(id string) bool { return typeOf(id).Support != SupportPlanned }

func sqlFamily(family string) bool {
	switch family {
	case familyMySQL, familyPostgres, familySQLite, familyOracle, familySQLServer, familyClickHouse:
		return true
	}
	return false
}

func schemaLevelFor(family string) bool {
	switch family {
	case familyPostgres, familySQLServer, familyOracle:
		return true
	}
	return false
}

func fileSource(id string) bool {
	switch id {
	case "sqlite", "duckdb", "access", "bigquery", "jdbc", "plugin":
		return true
	}
	return false
}

func databaseLabelFor(t DBType) string {
	switch t.Family {
	case familyRedis:
		return "默认库号（0-15）"
	case familyMongo:
		return "默认数据库（可选）"
	case familySQLite:
		return ""
	case familyOracle:
		if t.ID == "dameng" {
			return "默认模式（可选）"
		}
		return "服务名 / SID"
	case familySQLServer, familyClickHouse:
		return "数据库（可选）"
	default:
		return "默认数据库（可选）"
	}
}

func capsFor(t DBType) []string {
	if t.Support == SupportPlanned {
		return []string{}
	}
	switch t.Family {
	case familyRedis:
		return []string{"browse", "keys", "command", "export"}
	case familyMongo:
		return []string{"browse", "query", "command", "export"}
	}
	return []string{"query", "browse", "edit", "ddl", "import", "export", "explain", "sqlfile"}
}

// driverImpl 返回 database/sql 使用的驱动名。
func driverImpl(id string) string {
	t := typeOf(id)
	switch t.Family {
	case familyPostgres:
		return "pgx"
	case familySQLite:
		return "sqlite"
	case familyOracle:
		if t.ID == "dameng" {
			return "dm"
		}
		return "oracle"
	case familySQLServer:
		return "sqlserver"
	case familyClickHouse:
		return "clickhouse"
	default:
		return "mysql"
	}
}
