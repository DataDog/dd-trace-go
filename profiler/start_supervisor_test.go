// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package profiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/crashtrackerstate"
)

func profilerRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return activeProfiler != nil
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
	t.Run("pending", func(t *testing.T) {
		setSupervisorPending(t, true)

		require.NoError(t, Start(WithProfileTypes(HeapProfile)))
		assert.False(t, profilerRunning(), "Start started a profiler in the supervisor")

		crashtrackerstate.Release()
		assert.True(t, profilerRunning(), "Release did not start the deferred profiler")
	})

	t.Run("not pending", func(t *testing.T) {
		setSupervisorPending(t, false)

		require.NoError(t, Start(WithProfileTypes(HeapProfile)))
		assert.True(t, profilerRunning(), "Start did not start a profiler without the flag")
	})
}
