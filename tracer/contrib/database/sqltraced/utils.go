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

// Returns all information relative to the DSN:
// map[string]string{
// 	"user",
// 	"host",
// 	"port",
// 	"dbname",
// }
func parseDSN(driver interface{}, dsn string) (map[string]string, error) {
	switch driver.(type) {
	case *pq.Driver:
		return parsePostgresDSN(dsn)
	case *mysql.MySQLDriver:
		return parseMySQLDSN(dsn)
	}
	return nil, errors.New("DSN format unknown.")
}

func parsePostgresDSN(dsn string) (map[string]string, error) {
	if url, err := pq.ParseURL(dsn); err == nil {
		o := make(map[string]string)
		err = parseOpts(url, o)
		return o, err
	} else {
		return nil, err
	}
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
