// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ibm_sarama

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestConstructorsInMain covers sarama producers and consumers built in a
// package main, including under an import alias. The harness suites never
// build a package main.
func TestConstructorsInMain(t *testing.T) {
	agent := maintest.Run(t, "./ibm_sarama/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	count := map[string]int{}
	for _, s := range lister.Spans() {
		require.Equal(t, "IBM/sarama", s.Meta["component"], "unexpected span %s", s.Operation)
		count[s.Operation]++
	}
	// Three sync producers, two async producers and two consumers, each
	// handling one message.
	require.Equal(t, map[string]int{"kafka.produce": 5, "kafka.consume": 2}, count)
}
