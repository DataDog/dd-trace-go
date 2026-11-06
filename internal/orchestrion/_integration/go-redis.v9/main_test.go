// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package goredis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestClientsInMain covers go-redis clients created in package main, including
// one imported under another name. The harness suites never build a package
// main. Each command must report exactly one redis.command span.
func TestClientsInMain(t *testing.T) {
	agent := maintest.Run(t, "./go-redis.v9/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	commands := make(map[string]int)
	for _, span := range lister.Spans() {
		if span.Operation == "redis.command" && span.Meta["component"] == "redis/go-redis.v9" {
			commands[span.Meta["redis.raw_command"]]++
		}
	}
	for _, key := range []string{
		"main-new-client",
		"main-new-failover-client",
		"main-contrib-new-client",
		"main-new-cluster-client",
		"main-new-failover-cluster-client",
		"main-new-ring",
		"main-new-universal-client",
		"main-new-universal-cluster-client",
		"main-alias-new-client",
	} {
		assert.Equalf(t, 1, commands[getCommand(key)], "redis.command spans for %s", key)
	}
}
