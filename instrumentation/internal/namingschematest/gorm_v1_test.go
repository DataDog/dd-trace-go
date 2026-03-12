// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	gormtrace "github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var gormV1Test = harness.TestCase{
	Name: instrumentation.PackageGormIOGormV1,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []gormtrace.Option
		if serviceOverride != "" {
			opts = append(opts, gormtrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		db, err := gormtrace.Open(
			postgres.New(postgres.Config{
				DSN: "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
			}),
			&gorm.Config{},
			opts...,
		)
		require.NoError(t, err)

		var result int
		db.Raw("SELECT 1").Scan(&result)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"gorm.db"},
		DDService:       []string{"gorm.db"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	WantServiceSource: harness.ServiceSourceAssertions{
		Defaults:        []string{string(instrumentation.PackageGormIOGormV1)},
		ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "gorm.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "gorm.query", spans[0].OperationName())
	},
}
