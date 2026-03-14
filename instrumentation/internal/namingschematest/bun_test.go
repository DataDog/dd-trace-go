// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	buntrace "github.com/DataDog/dd-trace-go/contrib/uptrace/bun/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	_ "github.com/lib/pq"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var bunTest = harness.TestCase{
	Name: instrumentation.PackageUptraceBun,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []buntrace.Option
		if serviceOverride != "" {
			opts = append(opts, buntrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		sqldb, err := sql.Open("postgres", "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable")
		require.NoError(t, err)
		defer sqldb.Close()

		db := bun.NewDB(sqldb, pgdialect.New())
		buntrace.Wrap(db, opts...)

		var n int
		err = db.NewSelect().ColumnExpr("1").Scan(context.Background(), &n)
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"bun.db"},
		DDService:       []string{harness.TestDDService},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	WantServiceSource: harness.ServiceSourceAssertions{
		Defaults:        []string{string(instrumentation.PackageUptraceBun)},
		ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "bun.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "bun.query", spans[0].OperationName())
	},
}
