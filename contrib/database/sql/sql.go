// Package sql provides functions to trace the database/sql package (https://golang.org/pkg/database/sql).
// It will automatically augment operations such as connections, statements and transactions with tracing.
//
// We start by telling the package which driver we will be using. For example, if we are using "github.com/lib/pq",
// we would do as follows:
//
// 	sqltrace.Register("pq", pq.Driver{})
//	db, err := sqltrace.Open("pq", "postgres://pqgotest:password@localhost...")
//
// The rest of our application would continue as usual, but with tracing enabled.
//
package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql/internal"
)

var driverTypeToName = make(map[reflect.Type]string)
var nameToDriver = make(map[string]driver.Driver)
var nameToRegisterConfig = make(map[string]*registerConfig)

// Register tells the sql integration package about the driver that we will be tracing. It must
// be called before Open, if that connection is to be traced. It uses the driverName suffixed
// with ".db" as the default service name.
func Register(driverName string, driver driver.Driver, opts ...RegisterOption) {
	if driver == nil {
		panic("sqltrace: Register driver is nil")
	}
	if _, ok := nameToRegisterConfig[driverName]; ok {
		// already registered, don't change things
		return
	}
	typ := reflect.TypeOf(driver)
	driverTypeToName[typ] = driverName
	nameToDriver[driverName] = driver

	cfg := new(registerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = driverName + ".db"
	}
	nameToRegisterConfig[driverName] = cfg
	// sql.Register(driverName, driver)
}

// errNotRegistered is returned when there is an attempt to open a database connection towards a driver
// that has not previously been registered using this package.
var errNotRegistered = errors.New("sqltrace: Register must be called before Open")

type tracedConnector struct {
	connector  driver.Connector
	driverName string
	rc         *registerConfig
	oc         *openConfig
}

func (t *tracedConnector) Connect(c context.Context) (driver.Conn, error) {
	conn, err := t.connector.Connect(c)
	if err != nil {
		return nil, err
	}
	tp := &traceParams{
		driverName: t.driverName,
		rc:         t.rc,
		oc:         t.oc,
	}
	if dc, ok := t.connector.(*dsnConnector); ok {
		tp.meta, _ = internal.ParseDSN(t.driverName, dc.dsn)
	} else if t.oc.dsn != "" {
		tp.meta, _ = internal.ParseDSN(t.driverName, t.oc.dsn)
	}
	return &tracedConn{conn, tp}, err
}

func (t *tracedConnector) Driver() driver.Driver {
	return t.connector.Driver()
}

// from Go stdlib implementation of sql.Open
type dsnConnector struct {
	dsn    string
	driver driver.Driver
}

func (t dsnConnector) Connect(_ context.Context) (driver.Conn, error) {
	return t.driver.Open(t.dsn)
}

func (t dsnConnector) Driver() driver.Driver {
	return t.driver
}

// OpenDB returns connection to a DB using a the traced version of the given driver. In order for OpenDB
// to work, the driver must first be registered using Register. If this did not occur, OpenDB will panic.
func OpenDB(c driver.Connector, opts ...OpenOption) *sql.DB {
	name, ok := driverTypeToName[reflect.TypeOf(c.Driver())]
	if !ok {
		panic("sqltrace.OpenDB: driver is not registered via sqltrace.Register")
	}
	oc := new(openConfig)
	OpenOptions.defaults(oc)
	for _, fn := range opts {
		fn(oc)
	}
	tc := &tracedConnector{
		connector:  c,
		driverName: name,
		rc:         nameToRegisterConfig[name],
		oc:         oc,
	}
	return sql.OpenDB(tc)
}

// Open returns connection to a DB using a the traced version of the given driver. In order for Open
// to work, the driver must first be registered using Register. If this did not occur, Open will
// return an error.
func Open(driverName, dataSourceName string, opts ...OpenOption) (*sql.DB, error) {
	if _, ok := nameToRegisterConfig[driverName]; !ok {
		return nil, errNotRegistered
	}
	return OpenDB(&dsnConnector{dsn: dataSourceName, driver: nameToDriver[driverName]}, opts...), nil
}
