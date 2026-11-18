// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/crashtrackerstate"
)

func isNoopGlobalTracer() bool {
	_, ok := getGlobalTracer().(*NoopTracer)
	return ok
}

// setSupervisorPending runs the marker that the crashtracker Orchestrion
// aspect injects into package main, with a resolver that returns pending.
func setSupervisorPending(t *testing.T, pending bool) {
	t.Helper()
	crashtrackerstate.SetResolver(func() bool { return pending })
	crashtrackerstate.MarkSupervisorPending()
	t.Cleanup(func() {
		crashtrackerstate.Release()
		crashtrackerstate.SetResolver(nil)
		Stop()
	})
}

func TestStartWhileSupervisorPending(t *testing.T) {
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")

	t.Run("pending", func(t *testing.T) {
		setSupervisorPending(t, true)

		require.NoError(t, Start(WithAgentAddr("127.0.0.1:1")))
		assert.True(t, isNoopGlobalTracer(), "Start started a tracer in the supervisor")

		crashtrackerstate.Release()
		assert.False(t, isNoopGlobalTracer(), "Release did not start the deferred tracer")
	})

	t.Run("not pending", func(t *testing.T) {
		setSupervisorPending(t, false)

		require.NoError(t, Start(WithAgentAddr("127.0.0.1:1")))
		assert.False(t, isNoopGlobalTracer(), "Start did not start a tracer without the flag")
	})
}
