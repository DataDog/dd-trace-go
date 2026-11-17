// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gorm

import (
	"context"
	"testing"

	gormtrace "github.com/DataDog/dd-trace-go/contrib/gorm.io/gorm.v1/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseSpanCount runs TestCase and checks that no operation is traced twice.
type TestCaseSpanCount struct {
	TestCase
}

func (*TestCaseSpanCount) ExpectedSpanCount() int {
	return 16
}

// TestCaseContribOpen checks that a connection opened with the contrib's own
// Open is traced once. Open calls gorm.Open, which must not register the plugin
// on top of the callbacks Open registers itself.
type TestCaseContribOpen struct {
	db *gorm.DB
}

func (tc *TestCaseContribOpen) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.db, err = gormtrace.Open(sqlite.Open("file::memory:"), nil)
	require.NoError(t, err)
}

func (tc *TestCaseContribOpen) Run(ctx context.Context, t *testing.T) {
	runSelectOne(ctx, t, tc.db)
}

func (*TestCaseContribOpen) ExpectedTraces() trace.Traces {
	return selectOneTraces()
}

func (*TestCaseContribOpen) ExpectedSpanCount() int {
	return 6
}

// TestCasePluginConfig checks that a connection whose gorm.Config already holds
// the tracing plugin is traced once.
type TestCasePluginConfig struct {
	db *gorm.DB
}

func (tc *TestCasePluginConfig) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.db, err = gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		Plugins: map[string]gorm.Plugin{"DDTracePlugin": gormtrace.NewTracePlugin()},
	})
	require.NoError(t, err)
}

func (tc *TestCasePluginConfig) Run(ctx context.Context, t *testing.T) {
	runSelectOne(ctx, t, tc.db)
}

func (*TestCasePluginConfig) ExpectedTraces() trace.Traces {
	return selectOneTraces()
}

func (*TestCasePluginConfig) ExpectedSpanCount() int {
	return 6
}

func runSelectOne(ctx context.Context, t *testing.T, db *gorm.DB) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	var n int
	require.NoError(t, db.WithContext(ctx).Raw("SELECT 1").Scan(&n).Error)
	require.Equal(t, 1, n)
}

func selectOneTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"resource": "SELECT 1",
						"type":     "sql",
						"name":     "gorm.row_query",
						"service":  "gorm.db",
					},
					Meta: map[string]string{
						"component": "gorm.io/gorm.v1",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"resource": "SELECT 1",
								"type":     "sql",
								"name":     "sqlite3.query",
								"service":  "sqlite3.db",
							},
							Meta: map[string]string{
								"component": "database/sql",
							},
						},
					},
				},
			},
		},
	}
}
