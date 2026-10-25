package dbapp

// 数据库协议族：不同族在 DSN、分页、标识符转义与元数据查询上存在差异。
const (
	familyMySQL     = "mysql"
	familyPostgres  = "postgres"
	familySQLite    = "sqlite"
	familyOracle    = "oracle"
	familySQLServer = "sqlserver"
)

// DBType 描述一种可连接的数据库类型。
type DBType struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Family        string `json:"family"`
	Group         string `json:"group"`
	DefaultPort   int    `json:"defaultPort,omitempty"`
	SchemaLevel   bool   `json:"schemaLevel,omitempty"` // 库下是否还有模式层级
	File          bool   `json:"file,omitempty"`        // 是否使用本地数据库文件
	Color         string `json:"color"`
	DatabaseLabel string `json:"databaseLabel,omitempty"`
	Hint          string `json:"hint,omitempty"`
}

// dbTypes 按展示顺序分组列出支持的数据库类型。
var dbTypes = []DBType{
	// 常用
	{ID: "mysql", Name: "MySQL", Family: familyMySQL, Group: "常用", DefaultPort: 3306, Color: "#0ea5e9", DatabaseLabel: "默认数据库（可选）"},
	{ID: "mariadb", Name: "MariaDB", Family: familyMySQL, Group: "常用", DefaultPort: 3306, Color: "#0ea5e9", DatabaseLabel: "默认数据库（可选）"},
	{ID: "postgres", Name: "PostgreSQL", Family: familyPostgres, Group: "常用", DefaultPort: 5432, SchemaLevel: true, Color: "#336791", DatabaseLabel: "默认数据库（可选）"},
	{ID: "sqlite", Name: "SQLite", Family: familySQLite, Group: "常用", File: true, Color: "#10b981"},

	// 国产化
	{ID: "dm", Name: "达梦 DM", Family: familyOracle, Group: "国产化", DefaultPort: 5236, Color: "#0f766e", DatabaseLabel: "默认模式（可选）", Hint: "DM8，使用 Oracle 兼容语法"},
	{ID: "kingbase", Name: "人大金仓 KingbaseES", Family: familyPostgres, Group: "国产化", DefaultPort: 54321, SchemaLevel: true, Color: "#b91c1c", DatabaseLabel: "默认数据库（可选）", Hint: "PostgreSQL 兼容协议"},
	{ID: "opengauss", Name: "华为 openGauss", Family: familyPostgres, Group: "国产化", DefaultPort: 5432, SchemaLevel: true, Color: "#e11d48", DatabaseLabel: "默认数据库（可选）", Hint: "PostgreSQL 兼容协议"},
	{ID: "gaussdb", Name: "华为 GaussDB", Family: familyPostgres, Group: "国产化", DefaultPort: 8000, SchemaLevel: true, Color: "#e11d48", DatabaseLabel: "默认数据库（可选）", Hint: "PostgreSQL 兼容协议"},
	{ID: "uxdb", Name: "优炫 UXDB", Family: familyPostgres, Group: "国产化", DefaultPort: 5432, SchemaLevel: true, Color: "#0891b2", DatabaseLabel: "默认数据库（可选）", Hint: "PostgreSQL 兼容协议"},
	{ID: "vastbase", Name: "海量 Vastbase", Family: familyPostgres, Group: "国产化", DefaultPort: 5432, SchemaLevel: true, Color: "#7c3aed", DatabaseLabel: "默认数据库（可选）", Hint: "PostgreSQL 兼容协议"},
	{ID: "highgo", Name: "瀚高 HighGo", Family: familyPostgres, Group: "国产化", DefaultPort: 5866, SchemaLevel: true, Color: "#dc2626", DatabaseLabel: "默认数据库（可选）", Hint: "PostgreSQL 兼容协议"},
	{ID: "gbase8a", Name: "南大通用 GBase 8a", Family: familyMySQL, Group: "国产化", DefaultPort: 5258, Color: "#ea580c", DatabaseLabel: "默认数据库（可选）", Hint: "MySQL 兼容协议"},

	// 云与分布式
	{ID: "tidb", Name: "TiDB", Family: familyMySQL, Group: "云与分布式", DefaultPort: 4000, Color: "#dc2626", DatabaseLabel: "默认数据库（可选）", Hint: "MySQL 兼容协议"},
	{ID: "oceanbase", Name: "OceanBase（MySQL）", Family: familyMySQL, Group: "云与分布式", DefaultPort: 2881, Color: "#1e40af", DatabaseLabel: "默认数据库（可选）", Hint: "MySQL 模式；Oracle 模式请选择 Oracle 类型"},
	{ID: "polardb_mysql", Name: "PolarDB MySQL", Family: familyMySQL, Group: "云与分布式", DefaultPort: 3306, Color: "#f97316", DatabaseLabel: "默认数据库（可选）", Hint: "阿里云，MySQL 兼容协议"},
	{ID: "polardb_pg", Name: "PolarDB PostgreSQL", Family: familyPostgres, Group: "云与分布式", DefaultPort: 5432, SchemaLevel: true, Color: "#f97316", DatabaseLabel: "默认数据库（可选）", Hint: "阿里云，PostgreSQL 兼容协议"},
	{ID: "tdsql_mysql", Name: "TDSQL（MySQL）", Family: familyMySQL, Group: "云与分布式", DefaultPort: 3306, Color: "#2563eb", DatabaseLabel: "默认数据库（可选）", Hint: "腾讯云，MySQL 兼容协议"},

	// 其他
	{ID: "oracle", Name: "Oracle", Family: familyOracle, Group: "其他", DefaultPort: 1521, Color: "#c74634", DatabaseLabel: "服务名 / SID"},
	{ID: "sqlserver", Name: "SQL Server", Family: familySQLServer, Group: "其他", DefaultPort: 1433, SchemaLevel: true, Color: "#cc2927", DatabaseLabel: "数据库（可选）"},
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
	if t, ok := dbTypeIndex[id]; ok {
		return t
	}
	return dbTypes[0]
}

// familyOf 返回连接类型所属的协议族。
func familyOf(id string) string { return typeOf(id).Family }

// driverImpl 返回 database/sql 使用的驱动名。
func driverImpl(id string) string {
	if id == "dm" {
		return "dm"
	}
	switch familyOf(id) {
	case familyPostgres:
		return "pgx"
	case familySQLite:
		return "sqlite"
	case familyOracle:
		return "oracle"
	case familySQLServer:
		return "sqlserver"
	default:
		return "mysql"
	}
}
