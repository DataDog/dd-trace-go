// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package segmentio_kafka_v0

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestMessagesInMain covers a binary whose main package does not import the
// contrib. The suites in this package import it themselves, which would hide a
// build that fails to link the hooks.
func TestMessagesInMain(t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartKafkaTestContainer(t, nil)
	t.Setenv("KAFKA_ADDR", addr)
	t.Setenv("KAFKA_TOPIC", createTopic(t, addr, "main"))

	agent := maintest.Run(t, "./segmentio_kafka.v0/mainapp")
	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	// The app makes no other traced calls.
	spans := lister.Spans()
	produced := filter(spans, "kafka.produce")
	consumed := filter(spans, "kafka.consume")
	require.Len(t, produced, 2)
	require.Len(t, consumed, 2)
	require.Len(t, spans, 4)
	producedIDs := map[uint64]bool{}
	for _, s := range produced {
		producedIDs[s.SpanID] = true
	}
	for _, s := range consumed {
		assert.True(t, producedIDs[s.ParentID], "consume spans are children of produce spans")
	}
}
