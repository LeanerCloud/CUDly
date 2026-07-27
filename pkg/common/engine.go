package common

import "strings"

// engineNameMap maps database engine names to a consistent normalized format.
// AWS RIs use: "aurora-postgresql", "aurora-mysql", "mysql", "postgres"
// Cost Explorer uses: "Aurora PostgreSQL", "Aurora MySQL", "MySQL", "PostgreSQL"
var engineNameMap = map[string]string{
	// Cost Explorer format -> normalized
	"Aurora PostgreSQL": "aurora-postgresql",
	"Aurora MySQL":      "aurora-mysql",
	"MySQL":             "mysql",
	"PostgreSQL":        "postgresql",
	"MariaDB":           "mariadb",
	"Oracle":            "oracle",
	"SQL Server":        "sqlserver",
	// Already normalized (from AWS RIs)
	"aurora-postgresql": "aurora-postgresql",
	"aurora-mysql":      "aurora-mysql",
	"mysql":             "mysql",
	"postgresql":        "postgresql",
	"postgres":          "postgresql",
	"mariadb":           "mariadb",
	"oracle-se":         "oracle",
	"oracle-se1":        "oracle",
	"oracle-se2":        "oracle",
	"oracle-ee":         "oracle",
	"sqlserver-se":      "sqlserver",
	"sqlserver-ee":      "sqlserver",
	"sqlserver-ex":      "sqlserver",
	"sqlserver-web":     "sqlserver",
}

// NormalizeEngineName normalizes database engine names to a consistent format.
// Returns lowercase of the input as a fallback when the engine is not recognized.
func NormalizeEngineName(engine string) string {
	if normalized, ok := engineNameMap[engine]; ok {
		return normalized
	}
	return strings.ToLower(engine)
}

// EngineFromDetails extracts and normalizes the engine name from
// recommendation details. Returns an empty string for non-database/cache
// service types. DatabaseDetails/CacheDetails are always pointers (every
// producer constructs them that way; see service_details_codec.go's
// package doc for the invariant). The nil-pointer guards are not dead
// code: the `details == nil` check above only catches an untyped nil, so a
// typed nil reaches the switch and would panic on the field read.
func EngineFromDetails(details ServiceDetails) string {
	if details == nil {
		return ""
	}
	var engine string
	switch d := details.(type) {
	case *DatabaseDetails:
		if d == nil {
			return ""
		}
		engine = d.Engine
	case *CacheDetails:
		if d == nil {
			return ""
		}
		engine = d.Engine
	default:
		return ""
	}
	return NormalizeEngineName(engine)
}
