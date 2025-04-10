// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package leveldb provides functions to trace the syndtr/goleveldb package (https://github.com/syndtr/goleveldb).
package leveldb // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/syndtr/goleveldb/leveldb"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2/leveldb"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/storage"
)

// A DB wraps a leveldb.DB and traces all queries.
type DB = v2.DB

// Open calls leveldb.Open and wraps the resulting DB.
func Open(stor storage.Storage, o *opt.Options, opts ...Option) (*DB, error) {
	return v2.Open(stor, o, opts...)
}

// OpenFile calls leveldb.OpenFile and wraps the resulting DB.
func OpenFile(path string, o *opt.Options, opts ...Option) (*DB, error) {
	return v2.OpenFile(path, o, opts...)
}

// WrapDB wraps a leveldb.DB so that queries are traced.
func WrapDB(db *leveldb.DB, opts ...Option) *DB {
	return v2.WrapDB(db, opts...)
}

// A Snapshot wraps a leveldb.Snapshot and traces all queries.
type Snapshot = v2.Snapshot

// WrapSnapshot wraps a leveldb.Snapshot so that queries are traced.
func WrapSnapshot(snap *leveldb.Snapshot, opts ...Option) *Snapshot {
	return v2.WrapSnapshot(snap, opts...)
}

// A Transaction wraps a leveldb.Transaction and traces all queries.
type Transaction = v2.Transaction

// WrapTransaction wraps a leveldb.Transaction so that queries are traced.
func WrapTransaction(tr *leveldb.Transaction, opts ...Option) *Transaction {
	return v2.WrapTransaction(tr, opts...)
}

// An Iterator wraps a leveldb.Iterator and traces until Release is called.
type Iterator = v2.Iterator

// WrapIterator wraps a leveldb.Iterator so that queries are traced.
func WrapIterator(it iterator.Iterator, opts ...Option) *Iterator {
	return v2.WrapIterator(it, opts...)
}
