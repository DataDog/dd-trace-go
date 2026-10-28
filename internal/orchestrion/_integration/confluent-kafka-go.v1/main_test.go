// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestClientsInMain covers producers and consumers created in package main,
// including through an import under another name. The harness suites never
// build a package main. Two messages are produced and each of two consumers
// reads both.
func TestClientsInMain(t *testing.T) {
	agent := maintest.Run(t, "./confluent-kafka-go.v1/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	spans := make(map[string]int)
	for _, span := range lister.Spans() {
		if span.Meta["component"] == mockComponent {
			spans[span.Operation]++
		}
	}
	assert.Equal(t, map[string]int{"kafka.produce": 2, "kafka.consume": 4}, spans)
}
