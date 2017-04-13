package sql

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"

	"github.com/DataDog/dd-trace-go/tracer"
	"golang.org/x/net/context"
)

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
