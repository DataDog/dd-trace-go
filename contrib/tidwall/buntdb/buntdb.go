// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package buntdb

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2"

	"github.com/tidwall/buntdb"
)

// A DB wraps a buntdb.DB, automatically tracing any transactions.
type DB = v2.DB

// Open calls buntdb.Open and wraps the result.
func Open(path string, opts ...Option) (*DB, error) {
	return v2.Open(path, opts...)
}

// WrapDB wraps a buntdb.DB so it can be traced.
func WrapDB(db *buntdb.DB, opts ...Option) *DB {
	return v2.WrapDB(db, opts...)
}

// A Tx wraps a buntdb.Tx, automatically tracing any queries.
type Tx = v2.Tx

// WrapTx wraps a buntdb.Tx so it can be traced.
func WrapTx(tx *buntdb.Tx, opts ...Option) *Tx {
	return v2.WrapTx(tx, opts...)
}
