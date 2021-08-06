// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package pg

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestImplementsHook(t *testing.T) {
	var _ pg.QueryHook = (*queryHook)(nil)
}

func TestSelect(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	conn := pg.Connect(&pg.Options{
		User:     "postgres",
		Database: "postgres",
	})

	Wrap(conn)

	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
		tracer.ServiceName("fake-http-server"),
		tracer.SpanType(ext.SpanTypeWeb),
	)

	var n int
	// Using WithContext will make the postgres span a child of
	// the span inside ctx (parentSpan)
	res, err := conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
	parentSpan.Finish()
	spans := mt.FinishedSpans()

	require.NoError(t, err)
	assert.Equal(1, res.RowsAffected())
	assert.Equal(1, res.RowsReturned())
	assert.Equal(2, len(spans))
	assert.Equal(nil, err)
	assert.Equal(1, n)
	assert.Equal("go-pg", spans[0].OperationName())
	assert.Equal("http.request", spans[1].OperationName())
}

func TestServiceName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		conn := pg.Connect(&pg.Options{
			User:     "postgres",
			Database: "postgres",
		})

		Wrap(conn)

		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		// Using WithContext will make the postgres span a child of
		// the span inside ctx (parentSpan)
		res, err := conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		assert.Equal(1, res.RowsAffected())
		assert.Equal(1, res.RowsReturned())
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("go-pg", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("gopg.db", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
	})

	t.Run("global", func(t *testing.T) {
		globalconfig.SetServiceName("global-service")
		defer globalconfig.SetServiceName("")

		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		conn := pg.Connect(&pg.Options{
			User:     "postgres",
			Database: "postgres",
		})

		Wrap(conn)

		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		// Using WithContext will make the postgres span a child of
		// the span inside ctx (parentSpan)
		res, err := conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		assert.Equal(1, res.RowsAffected())
		assert.Equal(1, res.RowsReturned())
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("go-pg", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("global-service", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
	})

	t.Run("custom", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		conn := pg.Connect(&pg.Options{
			User:     "postgres",
			Database: "postgres",
		})

		Wrap(conn, WithServiceName("my-service-name"))

		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		// Using WithContext will make the postgres span a child of
		// the span inside ctx (parentSpan)
		res, err := conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
		parentSpan.Finish()
		spans := mt.FinishedSpans()

		require.NoError(t, err)
		assert.Equal(1, res.RowsAffected())
		assert.Equal(1, res.RowsReturned())
		assert.Equal(2, len(spans))
		assert.Equal(nil, err)
		assert.Equal(1, n)
		assert.Equal("go-pg", spans[0].OperationName())
		assert.Equal("http.request", spans[1].OperationName())
		assert.Equal("my-service-name", spans[0].Tag(ext.ServiceName))
		assert.Equal("fake-http-server", spans[1].Tag(ext.ServiceName))
	})
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		conn := pg.Connect(&pg.Options{
			User:     "postgres",
			Database: "postgres",
		})

		Wrap(conn, opts...)

		parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
			tracer.ServiceName("fake-http-server"),
			tracer.SpanType(ext.SpanTypeWeb),
		)

		var n int
		_, err := conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
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
