// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgxtrace "github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var jackcPGXV5Test = harness.TestCase{
	Name: instrumentation.PackageJackcPGXV5,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []pgxtrace.Option
		if serviceOverride != "" {
			opts = append(opts, pgxtrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()
		conn, err := pgxtrace.Connect(ctx, "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable", opts...)
		require.NoError(t, err)
		defer conn.Close(ctx)

		var n int
		err = conn.QueryRow(ctx, "SELECT 1").Scan(&n)
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"postgres.db"},
		DDService:       []string{"postgres.db"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	WantServiceSource: harness.ServiceSourceAssertions{
		Defaults:        []string{string(instrumentation.PackageJackcPGXV5)},
		ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "pgx.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "pgx.query", spans[0].OperationName())
	},
}
