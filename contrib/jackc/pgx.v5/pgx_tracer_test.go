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

func Test_Tracer(t *testing.T) {
	t.Run("Test_QueryTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		parent, ctx := tracer.StartSpanFromContext(ctx, "parent")

		opts := []Option{
			WithTraceQuery(true),
			WithTraceConnect(true),
			WithTracePrepare(true),
			WithTraceBatch(true),
			WithTraceCopyFrom(true),
			WithServiceName(defaultServiceName),
		}
		conn, err := Connect(ctx, postgresDSN, opts...)
		require.NoError(t, err)
		defer conn.Close(ctx)

		var x int

		err = conn.QueryRow(ctx, `SELECT 1`).Scan(&x)
		require.NoError(t, err)

		assert.Equal(t, 1, x)

		err = conn.QueryRow(ctx, `SELECT 2`).Scan(&x)
		require.NoError(t, err)

		assert.Equal(t, 2, x)

		batch := &pgx.Batch{}
		batch.Queue(`SELECT 1`)

		br := conn.SendBatch(ctx, batch)

		err = br.QueryRow().Scan(&x)
		require.NoError(t, err)

		err = br.Close()
		require.NoError(t, err)

		_, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)`)
		require.NoError(t, err)

		numbers := []int{1, 2, 3}

		copyFromSource := pgx.CopyFromSlice(len(numbers), func(i int) ([]any, error) {
			return []any{numbers[i]}, nil
		})

		_, err = conn.CopyFrom(ctx, []string{"numbers"}, []string{"number"}, copyFromSource)
		require.NoError(t, err)

		expectedFinishedSpans := 10

		assert.Len(t, mt.OpenSpans(), 1)
		assert.Len(t, mt.FinishedSpans(), expectedFinishedSpans)

		parent.Finish()
		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), expectedFinishedSpans+1)

		spans := mt.FinishedSpans()
		s0 := spans[0]
		assert.Equal(t, "pgx.connect", s0.OperationName())
		assert.Equal(t, "connect", s0.Tag(ext.ResourceName))
		checkDefaultTags(t, s0)

		s1 := spans[1]
		assert.Equal(t, "pgx.prepare", s1.OperationName())
		assert.Equal(t, `SELECT 1`, s1.Tag(ext.ResourceName))
		checkDefaultTags(t, s1)

		s2 := spans[2]
		assert.Equal(t, "pgx.query", s2.OperationName())
		assert.Equal(t, `SELECT 1`, s2.Tag(ext.ResourceName))
		checkDefaultTags(t, s2)

		s3 := spans[3]
		assert.Equal(t, "pgx.prepare", s3.OperationName())
		assert.Equal(t, `SELECT 2`, s3.Tag(ext.ResourceName))
		checkDefaultTags(t, s3)

		s4 := spans[4]
		assert.Equal(t, "pgx.query", s4.OperationName())
		assert.Equal(t, `SELECT 2`, s4.Tag(ext.ResourceName))
		checkDefaultTags(t, s4)

		s5 := spans[5]
		assert.Equal(t, "pgx.batch.query", s5.OperationName())
		assert.Equal(t, `SELECT 1`, s5.Tag(ext.ResourceName))
		checkDefaultTags(t, s5)

		s6 := spans[6]
		assert.Equal(t, "pgx.batch", s6.OperationName())
		assert.Equal(t, "pgx.batch", s6.Tag(ext.ResourceName))
		checkDefaultTags(t, s6)

		s7 := spans[7]
		assert.Equal(t, "pgx.query", s7.OperationName())
		assert.Equal(t, `CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)`, s7.Tag(ext.ResourceName))
		checkDefaultTags(t, s7)

		s8 := spans[8]
		assert.Equal(t, "pgx.prepare", s8.OperationName())
		assert.Equal(t, `select "number" from "numbers"`, s8.Tag(ext.ResourceName))
		checkDefaultTags(t, s8)

		s9 := spans[9]
		assert.Equal(t, "pgx.copyfrom", s9.OperationName())
		assert.Equal(t, "pgx.copyfrom", s9.Tag(ext.ResourceName))
		assert.Equal(t, pgx.Identifier([]string{"numbers"}), s9.Tag("tables"))
		assert.Equal(t, []string{"number"}, s9.Tag("columns"))
		checkDefaultTags(t, s9)

		s10 := spans[10]
		assert.Equal(t, "parent", s10.OperationName())
		assert.Equal(t, "parent", s10.Tag(ext.ResourceName))
	})
	t.Run("Test_BatchTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN)
		require.NoError(t, err)
		defer conn.Close(ctx)

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

		err = br.QueryRow().Scan(&b)
		require.NoError(t, err)

		err = br.QueryRow().Scan(&c)
		require.NoError(t, err)

		assert.Equal(t, a, 1)
		assert.Equal(t, b, 2)
		assert.Equal(t, c, 3)

		err = br.Close()
		require.NoError(t, err)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 5)

		spans := mt.FinishedSpans()
		s0 := spans[0]
		assert.Equal(t, "pgx.connect", s0.OperationName())
		assert.Equal(t, "connect", s0.Tag(ext.ResourceName))
		checkDefaultTags(t, s0)

		s1 := spans[1]
		assert.Equal(t, "pgx.batch.query", s1.OperationName())
		assert.Equal(t, `SELECT 1`, s1.Tag(ext.ResourceName))
		checkDefaultTags(t, s1)

		s2 := spans[2]
		assert.Equal(t, "pgx.batch.query", s2.OperationName())
		assert.Equal(t, `SELECT 2`, s2.Tag(ext.ResourceName))
		checkDefaultTags(t, s2)

		s3 := spans[3]
		assert.Equal(t, "pgx.batch.query", s3.OperationName())
		assert.Equal(t, `SELECT 3`, s3.Tag(ext.ResourceName))
		checkDefaultTags(t, s3)

		s4 := spans[4]
		assert.Equal(t, "pgx.batch", s4.OperationName())
		assert.Equal(t, "pgx.batch", s4.Tag(ext.ResourceName))
		checkDefaultTags(t, s4)
	})
	t.Run("Test_CopyFromTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN, WithTraceCopyFrom(true))
		require.NoError(t, err)
		defer conn.Close(ctx)

		_, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)`)
		require.NoError(t, err)

		numbers := []int{1, 2, 3}

		copyFromSource := pgx.CopyFromSlice(len(numbers), func(i int) ([]any, error) {
			return []any{numbers[i]}, nil
		})

		_, err = conn.CopyFrom(ctx, []string{"numbers"}, []string{"number"}, copyFromSource)
		require.NoError(t, err)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 4)

		spans := mt.FinishedSpans()
		s0 := spans[0]
		assert.Equal(t, "pgx.connect", s0.OperationName())
		assert.Equal(t, "connect", s0.Tag(ext.ResourceName))
		checkDefaultTags(t, s0)

		s1 := spans[1]
		assert.Equal(t, "pgx.query", s1.OperationName())
		assert.Equal(t, `CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)`, s1.Tag(ext.ResourceName))
		checkDefaultTags(t, s1)

		s2 := spans[2]

		assert.Equal(t, "pgx.prepare", s2.OperationName())
		assert.Equal(t, `select "number" from "numbers"`, s2.Tag(ext.ResourceName))
		checkDefaultTags(t, s2)

		s3 := spans[3]
		assert.Equal(t, "pgx.copyfrom", s3.OperationName())
		assert.Equal(t, "pgx.copyfrom", s3.Tag(ext.ResourceName))
		checkDefaultTags(t, s3)
		assert.Equal(t, pgx.Identifier([]string{"numbers"}), s3.Tag("tables"))
		assert.Equal(t, []string{"number"}, s3.Tag("columns"))

	})
	t.Run("Test_PrepareTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN, WithTracePrepare(true))
		require.NoError(t, err)
		defer conn.Close(ctx)

		_, err = conn.Prepare(ctx, "query", `SELECT 1`)
		require.NoError(t, err)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 2)

		spans := mt.FinishedSpans()
		s0 := spans[0]
		assert.Equal(t, "pgx.connect", s0.OperationName())
		assert.Equal(t, "connect", s0.Tag(ext.ResourceName))
		checkDefaultTags(t, s0)

		s1 := spans[1]
		assert.Equal(t, "pgx.prepare", s1.OperationName())
		assert.Equal(t, `SELECT 1`, s1.Tag(ext.ResourceName))
		checkDefaultTags(t, s1)
	})
	t.Run("Test_ConnectTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN, WithTraceConnect(true))
		require.NoError(t, err)
		defer conn.Close(ctx)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 1)

		spans := mt.FinishedSpans()
		s0 := spans[0]
		assert.Equal(t, "pgx.connect", s0.OperationName())
		assert.Equal(t, "connect", s0.Tag(ext.ResourceName))
		checkDefaultTags(t, s0)
	})

	t.Run("Test_DisableTracers", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(
			ctx,
			postgresDSN,
			WithTraceCopyFrom(false),
			WithTraceQuery(false),
			WithTraceConnect(false),
			WithTracePrepare(false),
			WithTraceBatch(false),
		)
		require.NoError(t, err)
		defer conn.Close(ctx)

		_, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS numbers (number INT NOT NULL)`)
		require.NoError(t, err)

		numbers := []int{1, 2, 3}

		copyFromSource := pgx.CopyFromSlice(len(numbers), func(i int) ([]any, error) {
			return []any{numbers[i]}, nil
		})

		_, err = conn.CopyFrom(ctx, []string{"numbers"}, []string{"number"}, copyFromSource)
		require.NoError(t, err)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 0)

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

		err = br.QueryRow().Scan(&b)
		require.NoError(t, err)

		err = br.QueryRow().Scan(&c)
		require.NoError(t, err)

		assert.Equal(t, a, 1)
		assert.Equal(t, b, 2)
		assert.Equal(t, c, 3)

		err = br.Close()
		require.NoError(t, err)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 0)
	})
}

func checkDefaultTags(t *testing.T, s mocktracer.Span) {
	assert.Equal(t, defaultServiceName, s.Tag(ext.ServiceName))
	assert.Equal(t, ext.SpanTypeSQL, s.Tag(ext.SpanType))
	assert.Equal(t, ext.DBSystemPostgreSQL, s.Tag(ext.DBSystem))
	assert.Equal(t, componentName, s.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindClient, s.Tag(ext.SpanKind))
}
