// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

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
	"math"
	"reflect"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// registeredDrivers holds a registry of all drivers registered via the sqltrace package.
var registeredDrivers = &driverRegistry{
	keys:    make(map[reflect.Type]string),
	drivers: make(map[string]driver.Driver),
	configs: make(map[string]*config),
}

type driverRegistry struct {
	// keys maps driver types to their registered names.
	keys map[reflect.Type]string
	// drivers maps keys to their registered driver.
	drivers map[string]driver.Driver
	// configs maps keys to their registered configuration.
	configs map[string]*config
}

// isRegistered reports whether the name matches an existing entry
// in the driver registry.
func (d *driverRegistry) isRegistered(name string) bool {
	_, ok := d.configs[name]
	return ok
}

// add adds the driver with the given name and config to the registry.
func (d *driverRegistry) add(name string, driver driver.Driver, cfg *config) {
	if d.isRegistered(name) {
		return
	}
	d.keys[reflect.TypeOf(driver)] = name
	d.drivers[name] = driver
	d.configs[name] = cfg
}

// name returns the name of the driver stored in the registry.
func (d *driverRegistry) name(driver driver.Driver) (string, bool) {
	name, ok := d.keys[reflect.TypeOf(driver)]
	return name, ok
}

// driver returns the driver stored in the registry with the provided name.
func (d *driverRegistry) driver(name string) (driver.Driver, bool) {
	driver, ok := d.drivers[name]
	return driver, ok
}

// config returns the config stored in the registry with the provided name.
func (d *driverRegistry) config(name string) (*config, bool) {
	config, ok := d.configs[name]
	return config, ok
}

// Register tells the sql integration package about the driver that we will be tracing. It must
// be called before Open, if that connection is to be traced. It uses the driverName suffixed
// with ".db" as the default service name.
func Register(driverName string, driver driver.Driver, opts ...RegisterOption) {
	if driver == nil {
		panic("sqltrace: Register driver is nil")
	}
	if registeredDrivers.isRegistered(driverName) {
		// already registered, don't change things
		return
	}

	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = driverName + ".db"
	}
	log.Debug("contrib/database/sql: Registering driver: %s %#v", driverName, cfg)
	registeredDrivers.add(driverName, driver, cfg)
}

// errNotRegistered is returned when there is an attempt to open a database connection towards a driver
// that has not previously been registered using this package.
var errNotRegistered = errors.New("sqltrace: Register must be called before Open")

type tracedConnector struct {
	connector  driver.Connector
	driverName string
	cfg        *config
}

func (t *tracedConnector) Connect(c context.Context) (driver.Conn, error) {
	conn, err := t.connector.Connect(c)
	if err != nil {
		return nil, err
	}
	tp := &traceParams{
		driverName: t.driverName,
		cfg:        t.cfg,
	}
	if dc, ok := t.connector.(*dsnConnector); ok {
		tp.meta, _ = internal.ParseDSN(t.driverName, dc.dsn)
	} else if t.cfg.dsn != "" {
		tp.meta, _ = internal.ParseDSN(t.driverName, t.cfg.dsn)
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
func OpenDB(c driver.Connector, opts ...Option) *sql.DB {
	name, ok := registeredDrivers.name(c.Driver())
	if !ok {
		panic("sqltrace.OpenDB: driver is not registered via sqltrace.Register")
	}
	rc, _ := registeredDrivers.config(name)
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	// use registered config for unset options
	if cfg.serviceName == "" {
		cfg.serviceName = rc.serviceName
	}
	if math.IsNaN(cfg.analyticsRate) {
		cfg.analyticsRate = rc.analyticsRate
	}
	tc := &tracedConnector{
		connector:  c,
		driverName: name,
		cfg:        cfg,
	}
	return sql.OpenDB(tc)
}

// Open returns connection to a DB using a the traced version of the given driver. In order for Open
// to work, the driver must first be registered using Register. If this did not occur, Open will
// return an error.
func Open(driverName, dataSourceName string, opts ...Option) (*sql.DB, error) {
	if !registeredDrivers.isRegistered(driverName) {
		return nil, errNotRegistered
	}
	d, _ := registeredDrivers.driver(driverName)
	return OpenDB(&dsnConnector{dsn: dataSourceName, driver: d}, opts...), nil
}
