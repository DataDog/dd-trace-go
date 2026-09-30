// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.
// Portions Copyright (c) 2026 CloudX. See LICENSE for the original MIT license.

package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type mockConn struct {
	clickhouse.Conn
	err    error
	batch  driver.Batch
	called string
	ctx    context.Context
	query  string
}

func (m *mockConn) call(ctx context.Context, method, query string) error {
	m.called, m.ctx, m.query = method, ctx, query
	return m.err
}

func (m *mockConn) Select(ctx context.Context, _ any, query string, _ ...any) error {
	return m.call(ctx, "Select", query)
}

func (m *mockConn) Query(ctx context.Context, query string, _ ...any) (driver.Rows, error) {
	return nil, m.call(ctx, "Query", query)
}

func (m *mockConn) QueryRow(ctx context.Context, query string, _ ...any) driver.Row {
	return &mockRow{err: m.call(ctx, "QueryRow", query)}
}

func (m *mockConn) Exec(ctx context.Context, query string, _ ...any) error {
	return m.call(ctx, "Exec", query)
}

func (m *mockConn) AsyncInsert(ctx context.Context, query string, _ bool, _ ...any) error {
	return m.call(ctx, "AsyncInsert", query)
}

func (m *mockConn) PrepareBatch(ctx context.Context, query string, _ ...driver.PrepareBatchOption) (driver.Batch, error) {
	return m.batch, m.call(ctx, "PrepareBatch", query)
}

func (m *mockConn) QueryFormat(ctx context.Context, _ string, query string, _ ...any) (io.ReadCloser, error) {
	return nil, m.call(ctx, "QueryFormat", query)
}

func (m *mockConn) InsertFormat(ctx context.Context, _ string, query string, _ io.Reader) error {
	return m.call(ctx, "InsertFormat", query)
}

func (m *mockConn) Ping(ctx context.Context) error { return m.call(ctx, "Ping", "") }
func (m *mockConn) Close() error {
	m.called = "Close"
	return m.err
}

type mockRow struct {
	driver.Row
	err error
}

func (r *mockRow) Err() error { return r.err }

type mockBatch struct {
	driver.Batch
	err    error
	called string
}

func (b *mockBatch) Send() error {
	b.called = "Send"
	return b.err
}

func (b *mockBatch) Flush() error {
	b.called = "Flush"
	return b.err
}

func (b *mockBatch) Abort() error {
	b.called = "Abort"
	return b.err
}

func TestSpanMetadata(t *testing.T) {
	for _, service := range []string{"", "custom-service"} {
		t.Run("override="+service, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			parent, ctx := tracer.StartSpanFromContext(t.Context(), "request", tracer.ServiceName("application"))
			defer parent.Finish()
			conn := Wrap(&mockConn{}, WithService(service), WithPeerService("analytics-prod"),
				WithHost("ch.example.com"), WithPort("9440"), WithDatabase("analytics"), WithUser("reader"))
			require.NoError(t, conn.Select(ctx, nil, "SELECT 1"))

			spans := mt.FinishedSpans()
			require.Equal(t, 1, len(spans))
			s := spans[0]
			assert.Equal(t, parent.Context().SpanID(), s.ParentID())
			if service == "" {
				assert.Equal(t, "application", s.Tag(ext.ServiceName))
			} else {
				assert.Equal(t, service, s.Tag(ext.ServiceName))
				assert.Equal(t, "opt.with_service", s.Tag(ext.KeyServiceSource))
			}
			for key, value := range map[string]string{
				ext.SpanType:              ext.SpanTypeSQL,
				ext.SpanKind:              ext.SpanKindClient,
				ext.Component:             "ClickHouse/clickhouse-go.v2",
				ext.DBSystem:              "clickhouse",
				ext.PeerService:           "analytics-prod",
				"_dd.peer.service.source": ext.PeerService,
				ext.TargetHost:            "ch.example.com",
				ext.TargetPort:            "9440",
				ext.DBName:                "analytics",
				ext.DBUser:                "reader",
			} {
				assert.Equal(t, value, s.Tag(key))
			}
			assert.Nil(t, s.Tag(ext.DBStatement))
			assert.Nil(t, s.Tag(ext.DBType))
		})
	}
}

func TestConnOperations(t *testing.T) {
	const query = "SELECT 1"
	operations := []struct {
		name, queryType string
		call            func(context.Context, clickhouse.Conn) error
	}{
		{"Select", "Query", func(ctx context.Context, c clickhouse.Conn) error { return c.Select(ctx, nil, query) }},
		{"Query", "Query", func(ctx context.Context, c clickhouse.Conn) error {
			_, err := c.Query(ctx, query)
			return err
		}},
		{"QueryRow", "Query", func(ctx context.Context, c clickhouse.Conn) error { return c.QueryRow(ctx, query).Err() }},
		{"Exec", "Exec", func(ctx context.Context, c clickhouse.Conn) error { return c.Exec(ctx, query) }},
		{"AsyncInsert", "Exec", func(ctx context.Context, c clickhouse.Conn) error { return c.AsyncInsert(ctx, query, true) }},
		{"PrepareBatch", "Prepare", func(ctx context.Context, c clickhouse.Conn) error {
			_, err := c.PrepareBatch(ctx, query)
			return err
		}},
		{"QueryFormat", "Query", func(ctx context.Context, c clickhouse.Conn) error {
			_, err := c.QueryFormat(ctx, "CSV", query)
			return err
		}},
		{"InsertFormat", "Exec", func(ctx context.Context, c clickhouse.Conn) error { return c.InsertFormat(ctx, "CSV", query, nil) }},
		{"Ping", "Ping", func(ctx context.Context, c clickhouse.Conn) error { return c.Ping(ctx) }},
	}
	for _, op := range operations {
		for _, driverErr := range []error{nil, errors.New("driver failure")} {
			t.Run(fmt.Sprintf("%s/error=%v", op.name, driverErr), func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				mock := &mockConn{err: driverErr, batch: &mockBatch{}}
				ctx := t.Context()
				assertDriverError(t, driverErr, op.call(ctx, Wrap(mock)))
				assert.Equal(t, op.name, mock.called)
				require.True(t, ctx == mock.ctx)
				resource := query
				if op.name == "Ping" {
					resource = "Ping"
					assert.Equal(t, "", mock.query)
				} else {
					assert.Equal(t, query, mock.query)
				}
				// Query and QueryFormat finish without reading or closing results.
				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				assert.Equal(t, "clickhouse.query", spans[0].OperationName())
				assert.Equal(t, op.queryType, spans[0].Tag("sql.query_type"))
				assert.Equal(t, resource, spans[0].Tag(ext.ResourceName))
				assertSpanError(t, spans[0], driverErr)
			})
		}
	}
}

func TestErrorCheckAndResourceOverride(t *testing.T) {
	for _, driverErr := range []error{nil, errors.New("driver failure")} {
		for _, report := range []bool{false, true} {
			t.Run(fmt.Sprintf("error=%v/report=%t", driverErr, report), func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				checked := false
				conn := Wrap(&mockConn{err: driverErr}, WithResourceName("fixed"), WithErrorCheck(func(err error) bool {
					assertDriverError(t, driverErr, err)
					checked = true
					return report
				}))
				assertDriverError(t, driverErr, conn.Exec(t.Context(), "SELECT 1"))
				assert.Equal(t, driverErr != nil, checked)
				spans := mt.FinishedSpans()
				require.Equal(t, 1, len(spans))
				assert.Equal(t, "fixed", spans[0].Tag(ext.ResourceName))
				if report {
					assertSpanError(t, spans[0], driverErr)
				} else {
					assertSpanError(t, spans[0], nil)
				}
			})
		}
	}
}

func TestBatchOperations(t *testing.T) {
	for _, method := range []string{"Send", "Flush", "Abort"} {
		for _, driverErr := range []error{nil, errors.New("driver failure"), clickhouse.ErrBatchAlreadySent} {
			t.Run(fmt.Sprintf("%s/error=%v", method, driverErr), func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				parent, ctx := tracer.StartSpanFromContext(t.Context(), "request")
				defer parent.Finish()
				mock := &mockBatch{err: driverErr}
				conn := Wrap(&mockConn{batch: mock}, WithPeerService("analytics-prod"))
				batch, err := conn.PrepareBatch(ctx, "INSERT INTO logs")
				require.NoError(t, err)
				call := map[string]func() error{"Send": batch.Send, "Flush": batch.Flush, "Abort": batch.Abort}[method]
				assertDriverError(t, driverErr, call())
				assert.Equal(t, method, mock.called)
				spans := mt.FinishedSpans()
				if method == "Abort" && errors.Is(driverErr, clickhouse.ErrBatchAlreadySent) {
					require.Equal(t, 1, len(spans))
					return
				}
				require.Equal(t, 2, len(spans))
				assert.Equal(t, "clickhouse.query", spans[1].OperationName())
				assert.Equal(t, "Batch"+method, spans[1].Tag("sql.query_type"))
				assert.Equal(t, "INSERT INTO logs", spans[1].Tag(ext.ResourceName))
				assert.Equal(t, "analytics-prod", spans[1].Tag(ext.PeerService))
				assert.Equal(t, parent.Context().SpanID(), spans[1].ParentID())
				assertSpanError(t, spans[1], driverErr)
			})
		}
	}
}

func TestUntracedOperations(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	assert.Nil(t, Wrap(nil))
	mock := &mockConn{err: errors.New("close failure")}
	assertDriverError(t, mock.err, Wrap(mock).Close())
	assert.Equal(t, "Close", mock.called)
	assert.Equal(t, 0, len(mt.FinishedSpans()))
}

func assertDriverError(t *testing.T, want, got error) {
	t.Helper()
	if want == nil {
		require.NoError(t, got)
	} else {
		require.ErrorIs(t, got, want)
	}
}

func assertSpanError(t *testing.T, span *mocktracer.Span, err error) {
	t.Helper()
	if err == nil {
		assert.Nil(t, span.Tag(ext.Error))
	} else {
		assert.Equal(t, err.Error(), span.Tag(ext.ErrorMsg))
	}
}

// Cached connection tags must remain immutable across concurrent operations.
func TestConcurrentSpanMetadata(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	conn := Wrap(&mockConn{}, WithService("analytics"), WithHost("localhost")).(*tracedConn)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			trace(t.Context(), conn.cfg, "Exec", fmt.Sprintf("SELECT %d", i), time.Now(), nil)
		})
	}
	wg.Wait()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 32)
	resources := make(map[any]bool)
	for _, span := range spans {
		assert.Equal(t, "analytics", span.Tag(ext.ServiceName))
		assert.Equal(t, "localhost", span.Tag(ext.TargetHost))
		resources[span.Tag(ext.ResourceName)] = true
	}
	assert.Len(t, resources, 32)
	assert.NotContains(t, conn.cfg.spanConfig.Tags, ext.ResourceName)
	assert.NotContains(t, conn.cfg.spanConfig.Tags, "sql.query_type")
}
