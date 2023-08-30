// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package leveldb

import (
	"testing"

	leveldbtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/syndtr/goleveldb/leveldb"

	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/storage"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type Integration struct {
	db       *leveldbtrace.DB
	numSpans int
	opts     []leveldbtrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]leveldbtrace.Option, 0),
	}
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, leveldbtrace.WithServiceName(name))
}

func (i *Integration) Name() string {
	return "syndtr/goleveldb/leveldb"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	var err error
	i.db, err = leveldbtrace.Open(storage.NewMemStorage(), &opt.Options{}, i.opts...)
	require.NoError(t, err)

	t.Cleanup(func() {
		i.db.Close()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.db.CompactRange(util.Range{})
	i.db.Delete([]byte("hello"), nil)
	i.db.Has([]byte("hello"), nil)
	i.db.Get([]byte("hello"), nil)
	var batch leveldb.Batch
	batch.Put([]byte("hello"), []byte("world"))
	i.db.Write(&batch, nil)
	i.numSpans += 5

	snapshot, err := i.db.GetSnapshot()
	require.NoError(t, err)
	defer snapshot.Release()
	snapshot.Get([]byte("hello"), nil)
	i.numSpans++

	transaction, err := i.db.OpenTransaction()
	require.NoError(t, err)
	transaction.Commit()
	i.numSpans++

	transaction, err = i.db.OpenTransaction()
	require.NoError(t, err)
	defer transaction.Discard()
	transaction.Get([]byte("hello"), nil)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
