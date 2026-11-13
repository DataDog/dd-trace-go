// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"
	"time"

	cassandra "github.com/gocql/gocql"
	"github.com/stretchr/testify/require"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that sessions are traced when gocql is imported
// under another name.
type TestCaseAliasedImport struct {
	base
	newSession *cassandra.Session
}

func (tc *TestCaseAliasedImport) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	// Checked here because the CreateSession hook would trace the session even
	// if the NewCluster call were not rewritten.
	cluster := cassandra.NewCluster(tc.hostPort)
	require.IsType(t, &gocqltrace.Observer{}, cluster.QueryObserver)
	var err error
	tc.session, err = cluster.CreateSession()
	require.NoError(t, err)
	t.Cleanup(func() { tc.session.Close() })

	tc.newSession, err = cassandra.NewSession(cassandra.ClusterConfig{
		Hosts:              []string{tc.hostPort},
		CQLVersion:         "3.0.0",
		Timeout:            11 * time.Second,
		ConnectTimeout:     11 * time.Second,
		NumConns:           1,
		Consistency:        cassandra.Quorum,
		PageSize:           5000,
		ConvictionPolicy:   &cassandra.SimpleConvictionPolicy{},
		ReconnectionPolicy: &cassandra.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
	})
	require.NoError(t, err)
	t.Cleanup(func() { tc.newSession.Close() })
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, tc.session.Query("SELECT cluster_name FROM system.local").WithContext(ctx).Exec())
	require.NoError(t, tc.newSession.Query("SELECT data_center FROM system.local").WithContext(ctx).Exec())
}

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
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
