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
	// Get the generic name of the driver
	driverName, err := sql.DriverName(driver)
	if err != nil {
		return nil, err
	}

	// Register a traced version of the driver
	Register(driverName, driver, getTracer(t))

	// Return a connection to our traced driver
	return OpenWithService(driverName, dsn, service)
}

// Register registers a traced version of the driver under the name `nameTraced`.
func Register(name string, driver driver.Driver, t ...*tracer.Tracer) {
	sql.Register(name, driver, t...)
}

// OpenWithService returns a traced version of *sqlx.DB.
func OpenWithService(driverName, dsn, service string) (*sqlx.DB, error) {
	db, err := sql.OpenWithService(driverName, dsn, service)
	if err != nil {
		return nil, err
	}

	// WARN: We need to call sqlx.NewDb with the original driver name (without the suffix "Traced"),
	// because under the hood sqlx uses the hardcoded original driver names to resolve the placeholders.
	// See the BindType() function for more information: github.com/jmoiron/sqlx/bind.go
	return sqlx.NewDb(db, sql.UntracedName(driverName)), err
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
