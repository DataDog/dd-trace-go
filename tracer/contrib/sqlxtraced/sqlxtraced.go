// Package sqlxtraced provides a traced version of the "jmoiron/sqlx" package
package sqlxtraced

import (
	"database/sql/driver"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
	"github.com/jmoiron/sqlx"
)

// Register registers a traced version of `driver`.
// See "github.com/DataDog/dd-trace-go/tracer/contrib/database/sqltraced" for more information.
func Register(driverName string, driver driver.Driver, trc *tracer.Tracer) {
	sqltraced.Register(driverName, driver, trc)
}

// Open returns a traced version of *sqlx.DB.
// User must necessarily use the Open function provided in this package
// to trace correctly the sqlx calls.
func Open(driverName, dataSourceName, service string) (*sqlx.DB, error) {
	db, err := sqltraced.Open(driverName, dataSourceName, service)
	if err != nil {
		return nil, err
	}
	return sqlx.NewDb(db, driverName), err
}
