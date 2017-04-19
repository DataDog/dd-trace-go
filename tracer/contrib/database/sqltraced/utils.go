package sqltraced

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"golang.org/x/net/context"
)

// Return all information passed through the DSN:
func parseDSN(driver interface{}, dsn string) (meta map[string]string, err error) {
	switch driver.(type) {
	case *pq.Driver:
		meta, err = parsePostgresDSN(dsn)
	case *mysql.MySQLDriver:
		meta, err = parseMySQLDSN(dsn)
	}
	meta = normalize(meta)
	return
}

func normalize(meta map[string]string) map[string]string {
	// Delete the entry "password" for security reasons
	delete(meta, "password")

	m := make(map[string]string)
	for k, v := range meta {
		m[normalizeKey(k)] = v
	}
	return m
}

func normalizeKey(k string) string {
	switch k {
	case "user":
		return "db." + k
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

func parsePostgresDSN(dsn string) (map[string]string, error) {
	var err error
	o := make(map[string]string)

	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		dsn, err = pq.ParseURL(dsn)
		if err != nil {
			return nil, err
		}
	}

	if err := parseOpts(dsn, o); err != nil {
		return nil, err
	}

	return o, nil
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

// Useful function returning a prefilled span
func getSpan(name, service, query string, args interface{}, tracer *tracer.Tracer, ctx context.Context) *tracer.Span {
	var values []driver.Value
	var namedValues []driver.NamedValue

	switch args.(type) {
	case []driver.Value:
		values = args.([]driver.Value)
	case []driver.NamedValue:
		namedValues = args.([]driver.NamedValue)
	default:
		return nil
	}

	span := tracer.NewChildSpanFromContext(name, ctx)
	span.Service = service
	span.Resource = query
	span.SetMeta("sql.query", query)
	if values != nil {
		span.SetMeta("args", fmt.Sprintf("%v", values))
		span.SetMeta("args_length", strconv.Itoa(len(values)))
	} else if namedValues != nil {
		span.SetMeta("args", fmt.Sprintf("%v", namedValues))
		span.SetMeta("args_length", strconv.Itoa(len(namedValues)))
	}

	return span
}

// Helper function copied from the database/sql package
func namedValueToValue(named []driver.NamedValue) ([]driver.Value, error) {
	dargs := make([]driver.Value, len(named))
	for n, param := range named {
		if len(param.Name) > 0 {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		dargs[n] = param.Value
	}
	return dargs, nil
}

// Check if a string is in a []string
func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}
