package sql

import (
	"database/sql"
	"database/sql/driver"

	"github.com/DataDog/dd-trace-go/tracer"
)

// Register makes a tracing wrapper of the driver available
// by the name "<given name>trace". A DB using this driver should be
// opened using OpenTraced
// If RegisterTraced is called twice with the same name or if driver
// is nil, it panics.
func Register(name string, driver driver.Driver) {
	if driver == nil {
		panic("sql: Register driver is nil")
	}

	t := tracer.NewTracer()
	tracedName := name + "trace"
	tracedDriver := NewTracedDriver(name, driver, t)

	sql.Register(tracedName, tracedDriver)
}

// Open function opens a DB using the tracing wrapper of the driver
func Open(name, service, dsn string) (*sql.DB, error) {
	tracedName := name + "trace"

	// Add tracing info in the DSN to pass it along until the driver.
	// TracedDriver will try to parse this info and use default parameters
	// otherwise
	tracedDSN := TracedDSN{
		service: service,
		dsn:     dsn,
	}
	return sql.Open(tracedName, tracedDSN.Format())
}
