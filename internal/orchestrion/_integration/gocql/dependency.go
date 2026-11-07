// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"

	"example.com/gocqldep"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/require"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that clusters and sessions built in a dependency
// module, not in the module being built, are traced.
type TestCaseDependency struct {
	base
	newSession *gocql.Session
}

func (tc *TestCaseDependency) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	cluster := gocqldep.NewCluster(tc.hostPort)
	require.IsType(t, &gocqltrace.Observer{}, cluster.QueryObserver)
	var err error
	tc.session, err = cluster.CreateSession()
	require.NoError(t, err)
	t.Cleanup(func() { tc.session.Close() })

	tc.newSession, err = gocqldep.NewSession(tc.hostPort)
	require.NoError(t, err)
	t.Cleanup(func() { tc.newSession.Close() })
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, tc.session.Query("SELECT cluster_name FROM system.local").WithContext(ctx).Exec())
	require.NoError(t, tc.newSession.Query("SELECT data_center FROM system.local").WithContext(ctx).Exec())
}

func (tc *TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				tc.querySpan("SELECT cluster_name FROM system.local"),
				tc.querySpan("SELECT data_center FROM system.local"),
			},
		},
	}
}

func (tc *TestCaseDependency) querySpan(resource string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "cassandra.query",
			"service":  "gocql.query",
			"resource": resource,
			"type":     "cassandra",
		},
		Meta: map[string]string{
			"component":                   "gocql/gocql",
			"span.kind":                   "client",
			"db.system":                   "cassandra",
			"out.port":                    tc.port,
			"db.cassandra.contact.points": tc.hostPort,
		},
	}
}
