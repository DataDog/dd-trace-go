// Package sqlxtraced provides a traced version of the "jmoiron/sqlx" package
package sqlxtraced

import (
	"database/sql/driver"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced/sqlutils"
	"github.com/jmoiron/sqlx"
)

// OpenTraced will first register the traced version of the `driver` if not yet and will then open a connection with it.
// This is usually the only function to use when there is no need for the granularity offered by Register and Open.
func OpenTraced(driver driver.Driver, dataSourceName, service string, trcv ...*tracer.Tracer) (*sqlx.DB, error) {
	driverName := sqlutils.GetDriverName(driver)
	Register(driverName, driver, trcv...)
	return Open(driverName, dataSourceName, service)
}

// Register registers a traced version of `driver`.
// See "github.com/DataDog/dd-trace-go/tracer/contrib/database/sqltraced" for more information.
func Register(driverName string, driver driver.Driver, trcv ...*tracer.Tracer) {
	sqltraced.Register(driverName, driver, trcv...)
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
