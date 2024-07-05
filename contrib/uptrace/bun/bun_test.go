// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package bun

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func setupDB(opts ...Option) *bun.DB {
	sqlite, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		panic(err)
	}

	db := bun.NewDB(sqlite, sqlitedialect.New())
	Wrap(db, opts...)

	return db
}

func TestImplementsHook(_ *testing.T) {
	var _ bun.QueryHook = (*queryHook)(nil)
}

func TestSelect(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	db := setupDB()
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
		tracer.ServiceName("fake-http-server"),
		tracer.SpanType(ext.SpanTypeWeb),
	)

	var n, rows int64
	// Using WithContext will make the postgres span a child of
	// the span inside ctx (parentSpan)
	res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
	parentSpan.Finish()
	spans := mt.FinishedSpans()

	require.NoError(t, err)
	rows, _ = res.RowsAffected()
	assert.Equal(int64(1), rows)
	assert.Equal(2, len(spans))
	assert.Equal(nil, err)
	assert.Equal(int64(1), n)
	assert.Equal("bun", spans[0].OperationName())
	assert.Equal("http.request", spans[1].OperationName())
	assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
	assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
}

func TestServiceName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		db := setupDB()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		rows, _ := res.RowsAffected()
		assert.Equal(int64(1), rows)
		assert.Len(spans, 2)
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("bun", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("bun.db", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
		assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
		assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
		assert.Equal(spans[0].ParentID(), spans[1].SpanID())
	})

	t.Run("global", func(t *testing.T) {
		prevName := globalconfig.ServiceName()
		defer globalconfig.SetServiceName(prevName)
		globalconfig.SetServiceName("global-service")

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		db := setupDB()
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		// Using WithContext will make the postgres span a child of
		// the span inside ctx (parentSpan)
		res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		rows, _ := res.RowsAffected()
		assert.Equal(int64(1), rows)
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("bun", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("global-service", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
		assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
		assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		db := setupDB(WithService("my-service-name"))
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		// Using WithContext will make the postgres span a child of
		// the span inside ctx (parentSpan)
		res, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		rows, _ := res.RowsAffected()
		assert.Equal(int64(1), rows)
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("bun", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("my-service-name", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
		assert.Equal("uptrace/bun", spans[0].Tag(ext.Component))
		assert.Equal(ext.DBSystemOtherSQL, spans[0].Tag(ext.DBSystem))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		db := setupDB(opts...)
		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		_, err := db.NewSelect().ColumnExpr("1").Exec(ctx, &n)
		parentSpan.Finish()

		require.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
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
