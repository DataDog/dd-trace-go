// Package sqlx provides a traced version of the "jmoiron/sqlx" package
// For more information about the API, see https://github.com/DataDog/dd-trace-go/contrib/database/sql.
package sqlx

import (
	"database/sql/driver"

	"github.com/DataDog/dd-trace-go/contrib/database/sql"
	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/jmoiron/sqlx"
)

// Open will first register the traced version of the `driver` if not yet registered and will then open a connection with it.
// This is usually the only function to use when there is no need for the granularity offered by Register and Open.
// The last argument is optional and allows you to pass a custom tracer.
func Open(driver driver.Driver, dsn, service string, t ...*tracer.Tracer) (*sqlx.DB, error) {
	// we first register the driver
	traceDriver := Register(driver, getTracer(t))

	// once the  driver is registered, we return the sqlx.DB to connect to our traced driver
	return OpenWithService(traceDriver, dsn, service)
}

// Register registers a traced version of `driver`.
func Register(driver driver.Driver, t *tracer.Tracer) (traceDriverName string) {
	return sql.Register(driver, t)
}

// OpenWithService returns a traced version of *sqlx.DB.
func OpenWithService(driverName, dsn, service string) (*sqlx.DB, error) {
	db, err := sql.OpenWithService(driverName, dsn, service)
	if err != nil {
		return nil, err
	}
	return sqlx.NewDb(db, driverName), err
}

// getTracer returns either the tracer passed as the last argument or a default tracer.
func getTracer(tracers []*tracer.Tracer) *tracer.Tracer {
	var t *tracer.Tracer
	if len(tracers) == 0 || (len(tracers) > 0 && tracers[0] == nil) {
		t = tracer.DefaultTracer
	} else {
		t = tracers[0]
	}
	return t
}
