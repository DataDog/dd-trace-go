package sqltraced

import (
	"strings"

	"github.com/DataDog/dd-trace-go/tracer/contrib/database/sqltraced/mysql"
	"github.com/DataDog/dd-trace-go/tracer/contrib/database/sqltraced/pq"
)

// parseDSN returns all information passed through the DSN:
func parseDSN(driverType, dsn string) (meta map[string]string, err error) {
	switch driverType {
	case "*pq.Driver":
		meta, err = parsePQDSN(dsn)
	case "*mysql.MySQLDriver":
		meta, err = parseMySQLDSN(dsn)
	}
	meta = normalize(meta)
	return meta, err
}

func normalize(meta map[string]string) map[string]string {
	m := make(map[string]string)
	for k, v := range meta {
		m[normalizeKey(k)] = v
	}
	return m
}

func normalizeKey(k string) string {
	switch k {
	case "user":
		return "db.user"
	case "application_name":
		return "db.application"
	case "dbname":
		return "db.name"
	case "host", "port":
		return "out." + k
	default:
		return "meta." + k
	}
}

func parsePQDSN(dsn string) (map[string]string, error) {
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

	// Ensure that we don't pass the password to the spans
	delete(meta, "password")

	return meta, nil
}

func parseMySQLDSN(dsn string) (map[string]string, error) {
	if cfg, err := mysql.ParseDSN(dsn); err == nil {
		addr := strings.Split(cfg.Addr, ":")
		o := map[string]string{
			"user":   cfg.User,
			"host":   addr[0],
			"port":   addr[1],
			"dbname": cfg.DBName,
		}
		return o, nil
	} else {
		return nil, err
	}
}
