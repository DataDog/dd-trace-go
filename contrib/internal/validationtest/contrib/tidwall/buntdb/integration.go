// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package buntdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/buntdb"

	buntdbtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/tidwall/buntdb"
)

type Integration struct {
	db       *buntdbtrace.DB
	numSpans int
	opts     []buntdbtrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]buntdbtrace.Option, 0),
	}
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, buntdbtrace.WithServiceName(name))
}

func (i *Integration) Name() string {
	return "tidwall/buntdb"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()
	i.db = getDatabase(t, i)

	t.Cleanup(func() {
		i.db.Close()
		i.numSpans = 0
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	i.db.View(func(tx *buntdbtrace.Tx) error {
		val, err := tx.Get("regular:a")
		assert.NoError(t, err)
		assert.Equal(t, "1", val)
		return nil
	})
	i.numSpans++

	i.db.View(func(tx *buntdbtrace.Tx) error {
		var arr []string
		err := tx.Descend("test-index", func(key, value string) bool {
			arr = append(arr, key, value)
			return true
		})
		assert.NoError(t, err)
		return nil
	})
	i.numSpans++

	i.db.View(func(tx *buntdbtrace.Tx) error {
		indexes, err := tx.Indexes()
		assert.NoError(t, err)
		assert.Equal(t, []string{"test-index", "test-spatial-index"}, indexes)
		return nil
	})
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func getDatabase(t *testing.T, i *Integration) *buntdbtrace.DB {
	bdb, err := buntdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	err = bdb.CreateIndex("test-index", "regular:*", buntdb.IndexBinary)
	if err != nil {
		t.Fatal(err)
	}

	err = bdb.CreateSpatialIndex("test-spatial-index", "spatial:*", buntdb.IndexRect)
	if err != nil {
		t.Fatal(err)
	}

	err = bdb.Update(func(tx *buntdb.Tx) error {
		tx.Set("regular:a", "1", nil)
		tx.Set("regular:b", "2", nil)
		tx.Set("regular:c", "3", nil)

		tx.Set("spatial:a", "[1 1]", nil)
		tx.Set("spatial:b", "[2 2]", nil)
		tx.Set("spatial:c", "[3 3]", nil)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return buntdbtrace.WrapDB(bdb, i.opts...)
}
