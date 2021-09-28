// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package leveldb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/storage"
	"github.com/syndtr/goleveldb/leveldb/util"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/CodapeWild/dd-trace-go.v1/internal/globalconfig"
)

func TestDB(t *testing.T) {
	testAction(t, "CompactRange", func(mt mocktracer.Tracer, db *DB) {
		db.CompactRange(util.Range{})
	})

	testAction(t, "Delete", func(mt mocktracer.Tracer, db *DB) {
		db.Delete([]byte("hello"), nil)
	})

	testAction(t, "Has", func(mt mocktracer.Tracer, db *DB) {
		db.Has([]byte("hello"), nil)
	})

	testAction(t, "Get", func(mt mocktracer.Tracer, db *DB) {
		db.Get([]byte("hello"), nil)
	})

	testAction(t, "Put", func(mt mocktracer.Tracer, db *DB) {
		span, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("my-http-server"))

		err := db.WithContext(ctx).
			Put([]byte("hello"), []byte("world"), nil)
		assert.NoError(t, err)

		span.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())
	})

	testAction(t, "Write", func(mt mocktracer.Tracer, db *DB) {
		var batch leveldb.Batch
		batch.Put([]byte("hello"), []byte("world"))
		db.Write(&batch, nil)
	})
}

func TestSnapshot(t *testing.T) {
	testAction(t, "Get", func(mt mocktracer.Tracer, db *DB) {
		snapshot, err := db.GetSnapshot()
		assert.NoError(t, err)
		defer snapshot.Release()

		snapshot.Get([]byte("hello"), nil)
	})

	testAction(t, "Has", func(mt mocktracer.Tracer, db *DB) {
		snapshot, err := db.GetSnapshot()
		assert.NoError(t, err)
		defer snapshot.Release()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("my-http-server"))

		_, err = snapshot.WithContext(ctx).
			Has([]byte("hello"), nil)
		assert.NoError(t, err)

		span.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())
	})
}

func TestTransaction(t *testing.T) {
	testAction(t, "Commit", func(mt mocktracer.Tracer, db *DB) {
		transaction, err := db.OpenTransaction()
		assert.NoError(t, err)
		transaction.Commit()
	})

	testAction(t, "Get", func(mt mocktracer.Tracer, db *DB) {
		transaction, err := db.OpenTransaction()
		assert.NoError(t, err)
		defer transaction.Discard()

		transaction.Get([]byte("hello"), nil)
	})

	testAction(t, "Has", func(mt mocktracer.Tracer, db *DB) {
		transaction, err := db.OpenTransaction()
		assert.NoError(t, err)
		defer transaction.Discard()

		span, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("my-http-server"))

		_, err = transaction.WithContext(ctx).
			Has([]byte("hello"), nil)
		assert.NoError(t, err)

		span.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())
	})
}

func TestIterator(t *testing.T) {
	testAction(t, "Iterator", func(mt mocktracer.Tracer, db *DB) {
		iterator := db.NewIterator(nil, nil)
		iterator.Release()
	})
}

func testAction(t *testing.T, name string, f func(mt mocktracer.Tracer, db *DB)) {
	t.Run(name, func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		db, err := Open(storage.NewMemStorage(), &opt.Options{},
			WithServiceName("my-database"))
		assert.NoError(t, err)
		defer db.Close()

		f(mt, db)

		spans := mt.FinishedSpans()
		assert.Equal(t, "leveldb.query", spans[0].OperationName())
		assert.Equal(t, ext.SpanTypeLevelDB, spans[0].Tag(ext.SpanType))
		assert.Equal(t, "my-database", spans[0].Tag(ext.ServiceName))
		assert.Equal(t, name, spans[0].Tag(ext.ResourceName))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		db, err := Open(storage.NewMemStorage(), &opt.Options{}, opts...)
		assert.NoError(t, err)
		defer db.Close()

		iterator := db.NewIterator(nil, nil)
		iterator.Release()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}
