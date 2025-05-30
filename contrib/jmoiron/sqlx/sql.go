// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package sqlx provides functions to trace the jmoiron/sqlx package (https://github.com/jmoiron/sqlx).
// To enable tracing, first use one of the "Register*" functions to register the sql driver that
// you will be using, then continue using the package as you normally would.
//
// For more information on registering and why this needs to happen, please check the
// github.com/DataDog/dd-trace-go/contrib/database/sql package.
package sqlx // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/jmoiron/sqlx"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/jmoiron/sqlx/v2"
	sqltraced "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"

	"github.com/jmoiron/sqlx"
)

// Open opens a new (traced) connection to the database using the given driver and source.
// Note that the driver must formerly be registered using database/sql integration's Register.
func Open(driverName, dataSourceName string, opts ...sqltraced.Option) (*sqlx.DB, error) {
	return v2.Open(driverName, dataSourceName, opts...)
}

// MustOpen is the same as Open, but panics on error.
// To get tracing, the driver must be formerly registered using the database/sql integration's
// Register.
func MustOpen(driverName, dataSourceName string) (*sqlx.DB, error) {
	return v2.MustOpen(driverName, dataSourceName)
}

// Connect connects to the data source using the given driver.
// To get tracing, the driver must be formerly registered using the database/sql integration's
// Register.
func Connect(driverName, dataSourceName string) (*sqlx.DB, error) {
	return v2.Connect(driverName, dataSourceName)
}

// MustConnect connects to a database and panics on error.
// To get tracing, the driver must be formerly registered using the database/sql integration's
// Register.
func MustConnect(driverName, dataSourceName string) *sqlx.DB {
	return v2.MustConnect(driverName, dataSourceName)
}
