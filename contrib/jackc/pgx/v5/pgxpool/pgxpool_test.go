package pgxpool

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pgConnString = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func Test_QueryTracer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := context.Background()

	conn, err := New(ctx, pgConnString)
	require.NoError(t, err)
	defer conn.Close()

	var x int

	err = conn.QueryRow(ctx, `select 1`).Scan(&x)
	require.NoError(t, err)

	assert.Equal(t, 1, x)

	err = conn.QueryRow(ctx, `select 2`).Scan(&x)
	require.NoError(t, err)

	assert.Equal(t, 2, x)

	assert.Len(t, mt.OpenSpans(), 0)
	assert.Len(t, mt.FinishedSpans(), 2)
}
