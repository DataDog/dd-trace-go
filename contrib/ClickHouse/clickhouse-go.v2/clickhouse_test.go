// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package clickhouse

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func TestClickHouse(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	testClickHouse(t, "127.0.0.1:9000")
}

func testClickHouse(t *testing.T, addr string) {
	options := &clickhouse.Options{Addr: []string{addr}}

	open, _ := clickhouse.Open(options)
	conn := WrapConnection(open, WithServiceName("test-clickhouse"))
	err := conn.Exec("DROP TABLE IF EXISTS example")
	require.NoError(t, err)
	err = conn.Exec("CREATE TABLE example (Col1 UInt64, Col2 String, Col3 Array(UInt8)) ENGINE = Memory")
	require.NoError(t, err)
	defer conn.Close()

	validateClickHouseSpan := func(t *testing.T, span mocktracer.Span, resourceName string) {
		assert.Equal(t, "test-clickhouse", span.Tag(ext.ServiceName),
			"service name should be set to test-clickhouse")
		assert.Equal(t, "clickhouse.query", span.OperationName(),
			"operation name should be set to clickhouse.query")
		assert.Equal(t, resourceName, span.Tag(ext.ResourceName),
			"resource name should be set to the clickhouse command")
	}

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		batch, _ := conn.PrepareBatch("INSERT INTO example (Col1, Col2, Col3)")

		i := 9999
		err := batch.Append(uint64(i), fmt.Sprintf("value_%d", i), make([]uint8, 0))
		require.NoError(t, err)
		err = batch.Send()
		require.NoError(t, err)

		var count uint64
		conn.QueryRow(`SELECT count() FROM example WHERE Col1 = ?`, i).Scan(
			&count,
		)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3)
		assert.Equal(t, uint64(1), count)
		validateClickHouseSpan(t, spans[2], `SELECT count() FROM example WHERE Col1 = ?`)
	})

	t.Run("context", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()
		span, ctx := tracer.StartSpanFromContext(ctx, "parent")
		_, err := conn.WithContext(ctx).Query(`SELECT * FROM example`)
		require.NoError(t, err)
		span.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		validateClickHouseSpan(t, spans[0], `SELECT * FROM example`)
		assert.Equal(t, span, spans[1])
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID(),
			"clickhouse span should be part of the parent trace")
	})

	t.Run("batch case", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()
		span, ctx := tracer.StartSpanFromContext(ctx, "parent")
		batch, err := conn.WithContext(ctx).PrepareBatch("INSERT INTO example")
		err = batch.Send()
		require.NoError(t, err)

		batch, err = conn.WithContext(ctx).PrepareBatch("INSERT INTO example")
		err = batch.Flush()
		require.NoError(t, err)

		err = batch.Abort()
		require.NoError(t, err)

		span.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 6)
		validateClickHouseSpan(t, spans[0], "INSERT INTO example")
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID(),
			"clickhouse span should be part of the parent trace")
	})

	t.Run("query case", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		_, err := conn.Query(`SELECT * FROM example`)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		validateClickHouseSpan(t, spans[0], `SELECT * FROM example`)
	})

	t.Run("select case", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		var result []struct {
			Col1 uint64 `ch:"number"`
		}
		err := conn.Select(&result, "SELECT number FROM system.numbers LIMIT 10")
		require.NoError(t, err)

		require.Len(t, result, 10)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		validateClickHouseSpan(t, spans[0], `SELECT number FROM system.numbers LIMIT 10`)
	})

	t.Run("with OLTP span propagation", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		options := &clickhouse.Options{Addr: []string{addr}}

		open, _ := clickhouse.Open(options)
		conn := WrapConnection(open, WithServiceName("test-clickhouse"), WithOLTPspan())
		_, err := conn.Query(`SELECT * FROM example`)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		validateClickHouseSpan(t, spans[0], `SELECT * FROM example`)
	})

	t.Run("stats case", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		options := &clickhouse.Options{Addr: []string{addr}}

		open, _ := clickhouse.Open(options)
		conn := WrapConnection(open, WithServiceName("test-clickhouse"), WithStats())

		// TODO check if asserts will always work
		for i := 0; i < 500; i++ {
			_, err = conn.Query(`SELECT * FROM example`)
		}

		require.NoError(t, err)

		spans := mt.FinishedSpans()
		span := spans[499]
		assert.Len(t, spans, 500)
		assert.Greater(t, span.Tag(ext.ClickHouseConnectionOpen), 0)
		assert.Greater(t, span.Tag(ext.ClickHouseConnectionOpen), 0)
		assert.Equal(t, 10, span.Tag(ext.ClickHouseMaxOpenConnections))
		assert.Equal(t, 5, span.Tag(ext.ClickHouseMaxIdleConnections))
		validateClickHouseSpan(t, span, `SELECT * FROM example`)
	})

	t.Run("with resource name", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		options := &clickhouse.Options{Addr: []string{addr}}

		open, _ := clickhouse.Open(options)
		conn := WrapConnection(open, WithServiceName("test-clickhouse"), WithResourceName("Query"))

		_, err := conn.Query(`SELECT * FROM example`)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		validateClickHouseSpan(t, spans[0], "Query")
	})
}

func TestAnalyticsSettings(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	addr := "127.0.0.1:9000"
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		options := &clickhouse.Options{Addr: []string{addr}}
		conn, _ := clickhouse.Open(options)
		client := WrapConnection(conn, opts...)

		err := client.WithContext(context.Background()).AsyncInsert(fmt.Sprintf(
			`INSERT INTO example VALUES (
		                            %d, '%s', [1, 2, 3, 4, 5, 6, 7, 8, 9], now())`,
			1, "Golang SQL database driver",
		), false)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, rate, spans[0].Tag(ext.EventSampleRate))
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

func Test_withOLTPSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span, _ := tracer.StartSpanFromContext(context.Background(), "parent")

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: uInt64ToTraceID(0, span.Context().TraceID()),
		SpanID:  uInt64ToSpanID(span.Context().SpanID()),
	})

	type args struct {
		span ddtrace.Span
	}
	tests := []struct {
		name string
		args args
		want trace.SpanContext
	}{
		{
			name: "translating datadog identifiers to OpenTelemetry",
			args: args{
				span: span,
			},
			want: spanContext,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spanContext := withOLTPSpanContext(tt.args.span)
			assert.NotNil(t, spanContext)
			assert.True(t, spanContext.HasSpanID())
			assert.True(t, spanContext.HasTraceID())

			t.Run(tt.name, func(t *testing.T) {
				assert.Equalf(t, tt.want, withOLTPSpanContext(tt.args.span), "withOLTPSpanContext(%v)", tt.args.span)
			})
		})
	}
}
