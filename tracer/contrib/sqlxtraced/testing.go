package sqlxtraced

import (
	"database/sql/driver"
	"log"

	"github.com/DataDog/dd-trace-go/tracer"
	"github.com/DataDog/dd-trace-go/tracer/contrib/sqltraced"
)

const debug = true

func newDB(name, service string, driver driver.Driver, dsn string) *sqltraced.DB {
	tracer, transport := tracer.GetTestTracer()
	tracer.DebugLoggingEnabled = debug

	dbx, err := OpenTraced(driver, dsn, service, tracer)
	if err != nil {
		log.Fatal(err)
	}

	return &sqltraced.DB{
		DB:        dbx.DB,
		Name:      name,
		Service:   service,
		Tracer:    tracer,
		Transport: transport,
	}
}
