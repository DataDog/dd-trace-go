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
//	sqltrace.Register("pq", &pq.Driver{})
//	db, err := sqltrace.Open("pq", "postgres://pqgotest:password@localhost...")
//
// The rest of our application would continue as usual, but with tracing enabled.
package sql

import (
	"database/sql"
	"database/sql/driver"

	v2 "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
)

// Register tells the sql integration package about the driver that we will be tracing. If used, it
// must be called before Open. It uses the driverName suffixed with ".db" as the default service
// name.
func Register(driverName string, driver driver.Driver, opts ...RegisterOption) {
	v2.Register(driverName, driver, opts...)
}

// OpenDB returns connection to a DB using the traced version of the given driver. The driver may
// first be registered using Register. If this did not occur, OpenDB will determine the driver name
// based on its type.
func OpenDB(c driver.Connector, opts ...Option) *sql.DB {
	return v2.OpenDB(c, opts...)
}

// Open returns connection to a DB using the traced version of the given driver. The driver may
// first be registered using Register. If this did not occur, Open will determine the driver by
// opening a DB connection and retrieving the driver using (*sql.DB).Driver, before closing it and
// opening a new, traced connection.
func Open(driverName, dataSourceName string, opts ...Option) (*sql.DB, error) {
	return v2.Open(driverName, dataSourceName, opts...)
}
