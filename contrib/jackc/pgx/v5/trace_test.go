package pgx

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tableName    = "testpgxv5"
	pgConnString = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	defer sqltest.Prepare(tableName)()
	os.Exit(m.Run())
}

func Test_Tracer(t *testing.T) {
	t.Run("Test_QueryTracer", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()

		conn, err := Connect(ctx, os.Getenv(pgConnString))
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

		conn, err := Connect(ctx, pgConnString, WithTraceBatch())
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

		conn, err := Connect(ctx, pgConnString, WithTraceCopyFrom())
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

		conn, err := Connect(ctx, pgConnString, WithTracePrepare())
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

		conn, err := Connect(ctx, pgConnString, WithTraceConnect())
		require.NoError(t, err)
		defer conn.Close(ctx)

		assert.Len(t, mt.OpenSpans(), 0)
		assert.Len(t, mt.FinishedSpans(), 1)
	})
}
