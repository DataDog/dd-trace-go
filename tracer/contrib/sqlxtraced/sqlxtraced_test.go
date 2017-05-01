package sqlxtraced

import (
	"database/sql/driver"
	"log"

	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
)

const DEBUG = true

func NewDB(name, service string, driver driver.Driver, dsn string) *sqltraced.DB {
	tracer, transport := sqltraced.GetTestTracer()
	tracer.DebugLoggingEnabled = DEBUG
	Register(name, service, driver, tracer)
	dbx, err := Open(name, dsn)
	if err != nil {
		log.Fatal(err)
	}

	return &sqltraced.DB{
		dbx.DB,
		name,
		service,
		tracer,
		transport,
	}
}
