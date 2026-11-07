// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gcppubsub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestMainApp covers Pub/Sub calls made from package main, including an admin
// client imported under another name. The harness suites never build a
// package main.
func TestMainApp(t *testing.T) {
	agent := maintest.Run(t, "./gcp_pubsub.v1/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	assert.Equal(t, map[string]int{
		"pubsub.publish":                        1,
		"pubsub.receive":                        1,
		"gcp.pubsub.request CreateTopic":        1,
		"gcp.pubsub.request CreateSubscription": 1,
		"gcp.pubsub.request ListTopics":         1,
		"gcp.pubsub.request ListSchemas":        1,
	}, countSpans(t, lister.Spans()))
}
