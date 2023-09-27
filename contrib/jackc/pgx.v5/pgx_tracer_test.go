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

func assertCommonTags(t *testing.T, s mocktracer.Span, qType string) {

}

func TestTraceQuery(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	opts := []Option{
		WithTraceQuery(true),
		WithTraceConnect(true),
		WithTracePrepare(true),
		WithTraceBatch(true),
		WithTraceCopyFrom(true),
	}
	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	conn, err := Connect(ctx, postgresDSN, opts...)
	require.NoError(t, err)

	query := fmt.Sprintf("SELECT id, name FROM %s LIMIT 5", tableName)
	_, err = conn.Query(ctx, query)
	require.NoError(t, err)

	span.Finish()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 3)

	s0 := spans[0]
	assert.Equal(t, "pgx.connect", s0.OperationName())
	assert.Equal(t, "Connect", s0.Tag(ext.ResourceName))

	s1 := spans[1]
	assert.Equal(t, "pgx.query", s1.OperationName())
	assert.Equal(t, query, s1.Tag(ext.ResourceName))

	s2 := spans[2]
	assert.Equal(t, "parent", s2.OperationName())
	assert.Equal(t, "parent", s2.Tag(ext.ResourceName))
}

func TestTraceBatch(t *testing.T) {

}

func Test_Tracer(t *testing.T) {
	t.Run("Test_QueryTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, os.Getenv(postgresDSN))
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

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 3)
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
		assert.Len(t, mt.FinishedSpans(), 4)
	})
	t.Run("Test_CopyFromTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN)
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
		assert.Len(t, mt.FinishedSpans(), 2)
	})
	t.Run("Test_PrepareTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN)
		require.NoError(t, err)
		defer conn.Close(ctx)

		_, err = conn.Prepare(ctx, "query", `SELECT 1`)
		require.NoError(t, err)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 1)
	})
	t.Run("Test_ConnectTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, postgresDSN)
		require.NoError(t, err)
		defer conn.Close(ctx)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 1)
	})
}
