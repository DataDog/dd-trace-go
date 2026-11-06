// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestClientsInMain covers go-elasticsearch clients created in package main,
// including some imported under another name. The harness suites never build
// a package main. Each request must report exactly one elasticsearch.query
// span.
func TestClientsInMain(t *testing.T) {
	agent := maintest.Run(t, "./go-elasticsearch/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	queries := make(map[string]int)
	services := make(map[string]string)
	for _, span := range lister.Spans() {
		if span.Operation == "elasticsearch.query" && span.Meta["component"] == "elastic/go-elasticsearch.v6" {
			queries[span.Resource]++
			services[span.Resource] = span.Service
		}
	}
	for _, key := range []string{
		"main-v6-new-client",
		"main-v6-new-default-client",
		"main-v7-new-client",
		"main-v7-new-default-client",
		"main-v8-new-client",
		"main-v8-new-typed-client",
		"main-v8-new-default-client",
		"main-v8-contrib-transport",
		"main-alias-v6-new-client",
		"main-alias-v7-new-client",
		"main-alias-v8-new-client",
	} {
		assert.Equalf(t, 1, queries["GET /"+key], "elasticsearch.query spans for %s", key)
	}
	assert.Equal(t, "contrib.client", services["GET /main-v8-contrib-transport"])
	// The GET / that each of the three v7 clients sends first.
	assert.Equal(t, 3, queries["GET /"])
	assert.Len(t, queries, 12)
}
