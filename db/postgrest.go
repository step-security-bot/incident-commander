package db

import (
	"github.com/flanksource/commons/deps"
	"github.com/flanksource/commons/logger"
)

var PostgRESTVersion = "v10.0.0"
var PostgRESTJWTSecret string
var PostgresDBAnonRole string

func GoOffline() error {
	return getBinary("")("--help")
}

func getBinary(port string) deps.BinaryFunc {
	return deps.BinaryWithEnv("postgREST", PostgRESTVersion, ".bin", map[string]string{
		"PGRST_SERVER_PORT":              port,
		"PGRST_DB_URI":                   ConnectionString,
		"PGRST_DB_SCHEMA":                Schema,
		"PGRST_DB_ANON_ROLE":             PostgresDBAnonRole,
		"PGRST_OPENAPI_SERVER_PROXY_URI": HttpEndpoint,
		"PGRST_LOG_LEVEL":                LogLevel,
		"PGRST_JWT_SECRET":               PostgRESTJWTSecret,
	})
}

func StartPostgrest(port string) {
	logger.Infof("Starting postgrest server on port %s", port)
	if err := getBinary(port)(""); err != nil {
		logger.Errorf("Failed to start postgREST: %v", err)
	}
}
