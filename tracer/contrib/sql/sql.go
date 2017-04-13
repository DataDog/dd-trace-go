package sql

import (
	"database/sql"
)

// Open function opens a DB using the tracing wrapper of the driver
func Open(name, service, dsn string) (*sql.DB, error) {
	// Add tracing info in the DSN to pass it along until the driver.
	// TracedDriver will try to parse this info and use default parameters
	// otherwise
	tracedDSN := TracedDSN{
		service: service,
		dsn:     dsn,
	}
	return sql.Open(name, tracedDSN.Format())
}
