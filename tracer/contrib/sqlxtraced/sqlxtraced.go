package sqlxtraced

import (
	"database/sql"
	"database/sql/driver"
	"strings"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/database/sqltraced"
	"github.com/jmoiron/sqlx"
)

// Register registers a traced version of the driver
func Register(name, service string, driver driver.Driver, trc *tracer.Tracer) {
	sqltraced.Register(strings.Title(name), service, driver, trc)
}

// Open returns a traced *sqlx.DB
func Open(driverName, dataSourceName string) (*sqlx.DB, error) {
	db, err := sql.Open(strings.Title(driverName), dataSourceName)
	if err != nil {
		return nil, err
	}
	return sqlx.NewDb(db, driverName), err
}
