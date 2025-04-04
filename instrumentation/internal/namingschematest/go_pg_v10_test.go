// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgtrace "github.com/DataDog/dd-trace-go/contrib/go-pg/pg.v10/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var goPGv10Test = harness.TestCase{
	Name: instrumentation.PackageGoPGV10,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []pgtrace.Option
		if serviceOverride != "" {
			opts = append(opts, pgtrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		conn := pg.Connect(&pg.Options{
			User:     "postgres",
			Password: "postgres",
			Database: "postgres",
		})
		pgtrace.Wrap(conn, opts...)
		defer conn.Close()

		var n int
		_, err := conn.QueryOne(pg.Scan(&n), "SELECT 1")
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"gopg.db"},
		DDService:       []string{harness.TestDDService},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "go-pg", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "postgresql.query", spans[0].OperationName())
	},
}
