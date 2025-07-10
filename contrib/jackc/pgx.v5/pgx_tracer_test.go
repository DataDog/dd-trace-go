// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tableName   = "testpgxv5"
	postgresDSN = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
)

func prepareDB() (func(), error) {
	queryDrop := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
	queryCreate := fmt.Sprintf("CREATE TABLE %s (id integer NOT NULL DEFAULT '0', name text)", tableName)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, postgresDSN)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, queryDrop); err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, queryCreate); err != nil {
		return nil, err
	}
	return func() {
		if _, err := conn.Exec(context.Background(), queryDrop); err != nil {
			log.Println(err)
		}
	}, nil
}

func TestMain(m *testing.M) {
	_, ok := env.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	cleanup, err := prepareDB()
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()
	os.Exit(m.Run())
}

func TestConnect(t *testing.T) {
	testCases := []struct {
		name           string
		newConnCreator func(t *testing.T, prev *pgxMockTracer) createConnFn
	}{
		{
			name: "pool",
			newConnCreator: func(_ *testing.T, _ *pgxMockTracer) createConnFn {
				opts := append(tracingAllDisabled(), WithTraceConnect(true))
				return newPoolCreator(nil, opts...)
			},
		},
		{
			name: "conn",
			newConnCreator: func(_ *testing.T, _ *pgxMockTracer) createConnFn {
				opts := append(tracingAllDisabled(), WithTraceConnect(true))
				return newConnCreator(nil, nil, opts...)
			},
		},
		{
			name: "conn_with_options",
			newConnCreator: func(_ *testing.T, _ *pgxMockTracer) createConnFn {
				opts := append(tracingAllDisabled(), WithTraceConnect(true))
				return newConnCreator(nil, &pgx.ParseConfigOptions{}, opts...)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			opts := append(tracingAllDisabled(), WithTraceConnect(true))
			runAllOperations(t, newPoolCreator(nil, opts...))

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)

			ps := spans[1]
			assert.Equal(t, "parent", ps.OperationName())
			assert.Equal(t, "parent", ps.Tag(ext.ResourceName))

			s := spans[0]
			assertCommonTags(t, s)
			assert.Equal(t, "pgx.connect", s.OperationName())
			assert.Equal(t, "Connect", s.Tag(ext.ResourceName))
			assert.Equal(t, "Connect", s.Tag("db.operation"))
			assert.Equal(t, nil, s.Tag(ext.DBStatement))
			assert.Equal(t, ps.SpanID(), s.ParentID())
		})
	}
}

func TestQuery(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceQuery(true))
	runAllOperations(t, newPoolCreator(nil, opts...))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 3)

	ps := spans[2]
	assert.Equal(t, "parent", ps.OperationName())
	assert.Equal(t, "parent", ps.Tag(ext.ResourceName))

	s := spans[0]
	assertCommonTags(t, s)
	assert.Equal(t, "pgx.query", s.OperationName())
	assert.Equal(t, "SELECT 1", s.Tag(ext.ResourceName))
	assert.Equal(t, "Query", s.Tag("db.operation"))
	assert.Equal(t, "SELECT 1", s.Tag(ext.DBStatement))
	assert.EqualValues(t, 1, s.Tag("db.result.rows_affected"))
	assert.Equal(t, ps.SpanID(), s.ParentID())

	s = spans[1]
	assertCommonTags(t, s)
	assert.Equal(t, "pgx.query", s.OperationName())
	assert.Equal(t, "CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)", s.Tag(ext.ResourceName))
	assert.Equal(t, "Query", s.Tag("db.operation"))
	assert.Equal(t, "CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)", s.Tag(ext.DBStatement))
	assert.EqualValues(t, 0, s.Tag("db.result.rows_affected"))
	assert.Equal(t, ps.SpanID(), s.ParentID())
}

func TestIgnoreError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceQuery(true), WithErrCheck(func(err error) bool {
		// Filter out errors that are not SQL error - undefined column or parameter name detected
		return strings.Contains(err.Error(), "SQLSTATE 42703")
	}))

	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()

	// Connect
	conn := newPoolCreator(nil, opts...)(t, ctx)

	// Query
	var x int
	err := conn.QueryRow(ctx, `SELECT 1`).Scan(x)
	require.Error(t, err)
	require.Equal(t, 0, x)

	err = conn.QueryRow(ctx, `SELECT unexisting_column`).Scan(x)
	require.Error(t, err)
	require.Equal(t, 0, x)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	require.Equal(t, nil, spans[0].Tag(ext.ErrorMsg))
	require.Equal(t, "ERROR: column \"unexisting_column\" does not exist (SQLSTATE 42703)", spans[1].Tag(ext.ErrorMsg))
}

func TestPrepare(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTracePrepare(true))
	runAllOperations(t, newPoolCreator(nil, opts...))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 3)

	ps := spans[2]
	assert.Equal(t, "parent", ps.OperationName())
	assert.Equal(t, "parent", ps.Tag(ext.ResourceName))

	s := spans[0]
	assertCommonTags(t, s)
	assert.Equal(t, "pgx.prepare", s.OperationName())
	assert.Equal(t, "SELECT 1", s.Tag(ext.ResourceName))
	assert.Equal(t, "Prepare", s.Tag("db.operation"))
	assert.Equal(t, "SELECT 1", s.Tag(ext.DBStatement))
	assert.EqualValues(t, nil, s.Tag("db.result.rows_affected"))
	assert.Equal(t, ps.SpanID(), s.ParentID())

	s = spans[1]
	assertCommonTags(t, s)
	assert.Equal(t, "pgx.prepare", s.OperationName())
	assert.Equal(t, "select \"number\" from \"numbers\"", s.Tag(ext.ResourceName))
	assert.Equal(t, "Prepare", s.Tag("db.operation"))
	assert.Equal(t, "select \"number\" from \"numbers\"", s.Tag(ext.DBStatement))
	assert.EqualValues(t, nil, s.Tag("db.result.rows_affected"))
	assert.Equal(t, ps.SpanID(), s.ParentID())
}

func TestBatch(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceBatch(true))
	runAllOperations(t, newPoolCreator(nil, opts...))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 5)

	ps := spans[4]
	assert.Equal(t, "parent", ps.OperationName())
	assert.Equal(t, "parent", ps.Tag(ext.ResourceName))

	batchSpan := spans[3]
	assertCommonTags(t, batchSpan)
	assert.Equal(t, "pgx.batch", batchSpan.OperationName())
	assert.Equal(t, "Batch", batchSpan.Tag(ext.ResourceName))
	assert.Equal(t, "Batch", batchSpan.Tag("db.operation"))
	assert.Equal(t, nil, batchSpan.Tag(ext.DBStatement))
	assert.EqualValues(t, nil, batchSpan.Tag("db.result.rows_affected"))
	assert.EqualValues(t, 3, batchSpan.Tag("db.batch.num_queries"))
	assert.Equal(t, ps.SpanID(), batchSpan.ParentID())

	s := spans[0]
	assert.Equal(t, "pgx.batch.query", s.OperationName())
	assert.Equal(t, "SELECT 1", s.Tag(ext.ResourceName))
	assert.Equal(t, "Query", s.Tag("db.operation"))
	assert.Equal(t, "SELECT 1", s.Tag(ext.DBStatement))
	assert.EqualValues(t, 1, s.Tag("db.result.rows_affected"))
	assert.Equal(t, batchSpan.SpanID(), s.ParentID())

	s = spans[1]
	assert.Equal(t, "pgx.batch.query", s.OperationName())
	assert.Equal(t, "SELECT 2", s.Tag(ext.ResourceName))
	assert.Equal(t, "Query", s.Tag("db.operation"))
	assert.Equal(t, "SELECT 2", s.Tag(ext.DBStatement))
	assert.EqualValues(t, 1, s.Tag("db.result.rows_affected"))
	assert.Equal(t, batchSpan.SpanID(), s.ParentID())

	s = spans[2]
	assert.Equal(t, "pgx.batch.query", s.OperationName())
	assert.Equal(t, "SELECT 3", s.Tag(ext.ResourceName))
	assert.Equal(t, "Query", s.Tag("db.operation"))
	assert.Equal(t, "SELECT 3", s.Tag(ext.DBStatement))
	assert.EqualValues(t, 1, s.Tag("db.result.rows_affected"))
	assert.Equal(t, batchSpan.SpanID(), s.ParentID())
}

func TestCopyFrom(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceCopyFrom(true))
	runAllOperations(t, newPoolCreator(nil, opts...))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	ps := spans[1]
	assert.Equal(t, "parent", ps.OperationName())
	assert.Equal(t, "parent", ps.Tag(ext.ResourceName))

	s := spans[0]
	assertCommonTags(t, s)
	assert.Equal(t, "pgx.copy_from", s.OperationName())
	assert.Equal(t, "Copy From", s.Tag(ext.ResourceName))
	assert.Equal(t, "Copy From", s.Tag("db.operation"))
	assert.Equal(t, nil, s.Tag(ext.DBStatement))
	assert.EqualValues(t, "numbers", s.Tag("db.copy_from.tables.0"))
	assert.EqualValues(t, "number", s.Tag("db.copy_from.columns.0"))
	assert.Equal(t, ps.SpanID(), s.ParentID())
}

func TestAcquire(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceAcquire(true))
	runAllOperations(t, newPoolCreator(nil, opts...))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 5)

	ps := spans[4]
	assert.Equal(t, "parent", ps.OperationName())
	assert.Equal(t, "parent", ps.Tag(ext.ResourceName))

	s := spans[0]
	assertCommonTags(t, s)
	assert.Equal(t, "pgx.pool.acquire", s.OperationName())
	assert.Equal(t, "Acquire", s.Tag(ext.ResourceName))
	assert.Equal(t, "Acquire", s.Tag("db.operation"))
	assert.Equal(t, nil, s.Tag(ext.DBStatement))
	assert.Equal(t, ps.SpanID(), s.ParentID())
}

// https://github.com/DataDog/dd-trace-go/issues/2908
func TestWrapTracer(t *testing.T) {
	testCases := []struct {
		name           string
		newConnCreator func(t *testing.T, prev *pgxMockTracer) createConnFn
		wantSpans      int
		wantHooks      int
	}{
		{
			name: "pool",
			newConnCreator: func(t *testing.T, prev *pgxMockTracer) createConnFn {
				cfg, err := pgxpool.ParseConfig(postgresDSN)
				require.NoError(t, err)
				cfg.ConnConfig.Tracer = prev
				return newPoolCreator(cfg)
			},
			wantSpans: 15,
			wantHooks: 13,
		},
		{
			name: "conn",
			newConnCreator: func(t *testing.T, prev *pgxMockTracer) createConnFn {
				cfg, err := pgx.ParseConfig(postgresDSN)
				require.NoError(t, err)
				cfg.Tracer = prev
				return newConnCreator(cfg, nil)
			},
			wantSpans: 11,
			wantHooks: 11, // 13 - 2 pool tracer hooks
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			prevTracer := &pgxMockTracer{
				called: make(map[string]bool),
			}
			runAllOperations(t, tc.newConnCreator(t, prevTracer))

			spans := mt.FinishedSpans()
			assert.Len(t, spans, tc.wantSpans)
			assert.Len(t, prevTracer.called, tc.wantHooks, "some hook(s) on the previous tracer were not called")
		})
	}
}

func tracingAllDisabled() []Option {
	return []Option{
		WithTraceConnect(false),
		WithTraceQuery(false),
		WithTracePrepare(false),
		WithTraceBatch(false),
		WithTraceCopyFrom(false),
		WithTraceAcquire(false),
		WithErrCheck(nil),
	}
}

type pgxConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

type createConnFn func(t *testing.T, ctx context.Context) pgxConn

func newPoolCreator(cfg *pgxpool.Config, opts ...Option) createConnFn {
	return func(t *testing.T, ctx context.Context) pgxConn {
		var (
			pool *pgxpool.Pool
			err  error
		)
		if cfg == nil {
			pool, err = NewPool(ctx, postgresDSN, opts...)
		} else {
			pool, err = NewPoolWithConfig(ctx, cfg, opts...)
		}
		require.NoError(t, err)
		t.Cleanup(func() {
			pool.Close()
		})
		return pool
	}
}

func newConnCreator(cfg *pgx.ConnConfig, connOpts *pgx.ParseConfigOptions, opts ...Option) createConnFn {
	return func(t *testing.T, ctx context.Context) pgxConn {
		var (
			conn *pgx.Conn
			err  error
		)
		if cfg != nil {
			conn, err = ConnectConfig(ctx, cfg, opts...)
		} else if connOpts != nil {
			conn, err = ConnectWithOptions(ctx, postgresDSN, *connOpts, opts...)
		} else {
			conn, err = Connect(ctx, postgresDSN, opts...)
		}
		require.NoError(t, err)
		t.Cleanup(func() {
			assert.NoError(t, conn.Close(ctx))
		})
		return conn
	}
}

func runAllOperations(t *testing.T, createConn createConnFn) {
	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()

	// Connect
	conn := createConn(t, ctx)

	// Query
	var x int
	err := conn.QueryRow(ctx, `SELECT 1`).Scan(&x)
	require.NoError(t, err)
	require.Equal(t, 1, x)

	// Batch
	batch := &pgx.Batch{}
	batch.Queue(`SELECT 1`)
	batch.Queue(`SELECT 2`)
	batch.Queue(`SELECT 3`)
	br := conn.SendBatch(ctx, batch)

	var (
		a int
		b int
		c int
	)
	err = br.QueryRow().Scan(&a)
	require.NoError(t, err)
	require.Equal(t, a, 1)
	err = br.QueryRow().Scan(&b)
	require.NoError(t, err)
	require.Equal(t, b, 2)
	err = br.QueryRow().Scan(&c)
	require.NoError(t, err)
	require.Equal(t, c, 3)

	err = br.Close()
	require.NoError(t, err)

	// Copy From
	_, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)`)
	require.NoError(t, err)
	numbers := []int{1, 2, 3}
	copyFromSource := pgx.CopyFromSlice(len(numbers), func(i int) ([]any, error) {
		return []any{numbers[i]}, nil
	})
	_, err = conn.CopyFrom(ctx, []string{"numbers"}, []string{"number"}, copyFromSource)
	require.NoError(t, err)
}

func assertCommonTags(t *testing.T, s *mocktracer.Span) {
	assert.Equal(t, defaultServiceName, s.Tag(ext.ServiceName))
	assert.Equal(t, ext.SpanTypeSQL, s.Tag(ext.SpanType))
	assert.Equal(t, ext.DBSystemPostgreSQL, s.Tag(ext.DBSystem))
	assert.Equal(t, string(instrumentation.PackageJackcPGXV5), s.Tag(ext.Component))
	assert.Equal(t, string(instrumentation.PackageJackcPGXV5), s.Integration())
	assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
	assert.Equal(t, "127.0.0.1", s.Tag(ext.NetworkDestinationName))
	assert.Equal(t, float64(5432), s.Tag(ext.NetworkDestinationPort))
	assert.Equal(t, "postgres", s.Tag(ext.DBName))
	assert.Equal(t, "postgres", s.Tag(ext.DBUser))
}

type pgxMockTracer struct {
	called map[string]bool
}

var (
	_ allPgxTracers = (*pgxMockTracer)(nil)
)

func (p *pgxMockTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	p.called["query.start"] = true
	return ctx
}

func (p *pgxMockTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	p.called["query.end"] = true
}

func (p *pgxMockTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	p.called["batch.start"] = true
	return ctx
}

func (p *pgxMockTracer) TraceBatchQuery(_ context.Context, _ *pgx.Conn, _ pgx.TraceBatchQueryData) {
	p.called["batch.query"] = true
}

func (p *pgxMockTracer) TraceBatchEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceBatchEndData) {
	p.called["batch.end"] = true
}

func (p *pgxMockTracer) TraceConnectStart(ctx context.Context, _ pgx.TraceConnectStartData) context.Context {
	p.called["connect.start"] = true
	return ctx
}

func (p *pgxMockTracer) TraceConnectEnd(_ context.Context, _ pgx.TraceConnectEndData) {
	p.called["connect.end"] = true
}

func (p *pgxMockTracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, _ pgx.TracePrepareStartData) context.Context {
	p.called["prepare.start"] = true
	return ctx
}

func (p *pgxMockTracer) TracePrepareEnd(_ context.Context, _ *pgx.Conn, _ pgx.TracePrepareEndData) {
	p.called["prepare.end"] = true
}

func (p *pgxMockTracer) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromStartData) context.Context {
	p.called["copyfrom.start"] = true
	return ctx
}

func (p *pgxMockTracer) TraceCopyFromEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceCopyFromEndData) {
	p.called["copyfrom.end"] = true
}

func (p *pgxMockTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	p.called["pool.acquire.start"] = true
	return ctx
}

func (p *pgxMockTracer) TraceAcquireEnd(_ context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	p.called["pool.acquire.end"] = true
}
