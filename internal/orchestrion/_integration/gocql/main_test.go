// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestSessionsInMain covers gocql sessions built in package main, including
// with gocql imported under another name. The harness suites never build a
// package main. Each query must report exactly one cassandra.query span.
func TestSessionsInMain(t *testing.T) {
	maintest.RequireOtelc(t)
	var b base
	b.setup(context.Background(), t)
	t.Setenv("GOCQL_TEST_HOST", b.hostPort)

	agent := maintest.Run(t, "./gocql/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	queries := make(map[string]int)
	for _, span := range lister.Spans() {
		if span.Operation == querySpan && span.Meta["component"] == componentName {
			queries[span.Resource]++
		}
	}
	for _, alias := range []string{
		"main_new_cluster",
		"main_struct_literal",
		"main_new_session",
		"main_contrib_traced",
		"main_contrib_deprecated",
		"main_alias_new_cluster",
	} {
		assert.Equalf(t, 1, queries["SELECT key AS "+alias+" FROM system.local"], "cassandra.query spans for %s", alias)
	}
}
