// Package sqlx provides functions to trace the jmoiron/sqlx package (https://github.com/jmoiron/sqlx).
// To enable tracing, first use one of the "Register*" functions to register the sql driver that
// you will be using, then continue using the package as you normally would.
//
// For more information on registering and why this needs to happen, please check the
// github.com/DataDog/dd-trace-go/contrib/database/sql package.
//
package sqlx

import (
	"database/sql/driver"

	sqltraced "github.com/DataDog/dd-trace-go/contrib/database/sql"

	"github.com/jmoiron/sqlx"
)

// Register tells the sqlx integration package about the driver that we will be tracing. Internally it
// registers a new version of the driver that is augmented with tracing. It must be called before
// Open, if that connection is to be traced. It uses the driverName suffixed with ".db" as the
// default service name. To set a custom service name, use RegisterWithServiceName.
func Register(driverName string, driver driver.Driver) { sqltraced.Register(driverName, driver) }

// RegisterWithServiceName performs the same operation as Register, but it allows setting a custom service name.
func RegisterWithServiceName(serviceName, driverName string, driver driver.Driver) {
	sqltraced.Register(driverName, driver, sqltraced.WithServiceName(serviceName))
}

func Open(driverName, dataSourceName string) (*sqlx.DB, error) {
	db, err := sqltraced.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	return sqlx.NewDb(db, driverName), nil
}

// MustOpen is the same as Open, but panics on error.
func MustOpen(driverName, dataSourceName string) (*sqlx.DB, error) {
	db, err := sqltraced.Open(driverName, dataSourceName)
	if err != nil {
		panic(err)
	}
	return sqlx.NewDb(db, driverName), nil
}

func Connect(driverName, dataSourceName string) (*sqlx.DB, error) {
	db, err := Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// MustConnect connects to a database and panics on error.
func MustConnect(driverName, dataSourceName string) *sqlx.DB {
	db, err := Connect(driverName, dataSourceName)
	if err != nil {
		panic(err)
	}
	return db
}
