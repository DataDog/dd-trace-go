// Package parsedsn provides functions to parse any kind of DSNs into a map[string]string
package parsedsn

import (
	"strings"

	"github.com/DataDog/dd-trace-go/contrib/database/sql/parsedsn/mysql"
	"github.com/DataDog/dd-trace-go/contrib/database/sql/parsedsn/pq"
)

// ParseDSN returns a map of string containing all normalized information passed through the DSN.
func ParseDSN(driverName, dsn string) (meta map[string]string, err error) {
	switch driverName {
	case "mysql":
		meta, err = ParseMySQL(dsn)
	case "postgres":
		meta, err = ParsePostgres(dsn)
	}
	meta = normalize(meta)
	return meta, err
}

func normalize(meta map[string]string) map[string]string {
	m := make(map[string]string)
	for k, v := range meta {
		if nk, ok := normalizeKey(k); ok {
			m[nk] = v
		}
	}
	return m
}

func normalizeKey(k string) (string, bool) {
	switch k {
	case "user":
		return "db.user", true
	case "application_name":
		return "db.application", true
	case "dbname":
		return "db.name", true
	case "host", "port":
		return "out." + k, true
	default:
		return "", false
	}
}

// ParsePostgres parses a postgres-type dsn into a map
func ParsePostgres(dsn string) (map[string]string, error) {
	var err error
	meta := make(map[string]string)

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		dsn, err = pq.ParseURL(dsn)
		if err != nil {
			return nil, err
		}
	}

	if err := pq.ParseOpts(dsn, meta); err != nil {
		return nil, err
	}

	// Assure that we do not pass the user secret
	delete(meta, "password")

	return meta, nil
}

// ParseMySQL parses a mysql-type dsn into a map
func ParseMySQL(dsn string) (m map[string]string, err error) {
	var cfg *mysql.Config
	if cfg, err = mysql.ParseDSN(dsn); err == nil {
		addr := strings.Split(cfg.Addr, ":")
		m = map[string]string{
			"user":   cfg.User,
			"host":   addr[0],
			"port":   addr[1],
			"dbname": cfg.DBName,
		}
		return m, nil
	}
	return nil, err
}
