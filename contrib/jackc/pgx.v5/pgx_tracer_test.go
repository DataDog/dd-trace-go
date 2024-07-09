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
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/jackc/pgx/v5"
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
	_, ok := os.LookupEnv("INTEGRATION")
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
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceConnect(true))
	runAllOperations(t, opts...)

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
}

func TestQuery(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceQuery(true))
	runAllOperations(t, opts...)

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

func TestPrepare(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTracePrepare(true))
	runAllOperations(t, opts...)

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
	runAllOperations(t, opts...)

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
	runAllOperations(t, opts...)

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
	assert.EqualValues(t, []string{"numbers"}, s.Tag("db.copy_from.tables"))
	assert.EqualValues(t, []string{"number"}, s.Tag("db.copy_from.columns"))
	assert.Equal(t, ps.SpanID(), s.ParentID())
}

func TestAcquire(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := append(tracingAllDisabled(), WithTraceAcquire(true))
	runAllOperations(t, opts...)

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

func tracingAllDisabled() []Option {
	return []Option{
		WithTraceConnect(false),
		WithTraceQuery(false),
		WithTracePrepare(false),
		WithTraceBatch(false),
		WithTraceCopyFrom(false),
		WithTraceAcquire(false),
	}
}

func runAllOperations(t *testing.T, opts ...Option) {
	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	defer parent.Finish()

	// Connect
	conn, err := NewPool(ctx, postgresDSN, opts...)
	require.NoError(t, err)
	defer conn.Close()

	// Query
	var x int
	err = conn.QueryRow(ctx, `SELECT 1`).Scan(&x)
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

func assertCommonTags(t *testing.T, s mocktracer.Span) {
	assert.Equal(t, defaultServiceName, s.Tag(ext.ServiceName))
	assert.Equal(t, ext.SpanTypeSQL, s.Tag(ext.SpanType))
	assert.Equal(t, ext.DBSystemPostgreSQL, s.Tag(ext.DBSystem))
	assert.Equal(t, componentName, s.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
	assert.Equal(t, "127.0.0.1", s.Tag(ext.NetworkDestinationName))
	assert.Equal(t, 5432, s.Tag(ext.NetworkDestinationPort))
	assert.Equal(t, "postgres", s.Tag(ext.DBName))
	assert.Equal(t, "postgres", s.Tag(ext.DBUser))
}
