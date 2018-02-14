package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/DataDog/dd-trace-go/contrib/database/sql/internal"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/ext"
)

var _ driver.Driver = (*tracedDriver)(nil)

// tracedDriver wraps an inner sql driver with tracing. It implements the (database/sql).driver.Driver interface.
type tracedDriver struct {
	driver.Driver
	driverName string
	config     *registerConfig
}

// Open returns a tracedConn so that we can pass all the info we get from the DSN
// all along the tracing
func (d *tracedDriver) Open(dsn string) (c driver.Conn, err error) {
	var (
		meta map[string]string
		conn driver.Conn
	)
	meta, err = internal.ParseDSN(d.driverName, dsn)
	if err != nil {
		return nil, err
	}
	conn, err = d.Driver.Open(dsn)
	if err != nil {
		return nil, err
	}
	d.config.tracer.SetServiceInfo(d.config.serviceName, d.driverName, ext.AppTypeDB)
	tp := &traceParams{
		driverName: d.driverName,
		config:     d.config,
		meta:       meta,
	}
	return &tracedConn{conn, tp}, err
}

// traceParams stores all information relative to the tracing
type traceParams struct {
	config     *registerConfig
	driverName string
	resource   string
	meta       map[string]string
}

func (tp *traceParams) newChildSpanFromContext(ctx context.Context, resource string, query string) *tracer.Span {
	name := fmt.Sprintf("%s.query", tp.driverName)
	span := tp.config.tracer.NewChildSpanFromContext(name, ctx)
	span.Type = ext.SQLType
	span.Service = tp.config.serviceName
	span.Resource = resource
	if query != "" {
		span.Resource = query
		span.SetMeta(ext.SQLQuery, query)
	}
	for k, v := range tp.meta {
		span.SetMeta(k, v)
	}
	return span
}

// tracedDriverName returns the name of the traced version for the given driver name.
func tracedDriverName(name string) string { return name + ".traced" }

// driverExists returns true if the given driver name has already been registered.
func driverExists(name string) bool {
	for _, v := range sql.Drivers() {
		if name == v {
			return true
		}
	}
	return false
}
