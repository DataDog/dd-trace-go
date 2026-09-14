// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gls

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
)

// TestSpanPoolEnabledUnderOrchestrion is the inverse of the gate this file used
// to assert, and it exists because that gate is now gone.
//
// The span pool was force-disabled whenever Orchestrion's GLS weave was active:
// the "this entry is dead" bit lived on the span as __dd_glsReclaimable, and
// Span.clear reset it when the pool recycled the span, so a recycled span could
// hand a stale liveness bit to a scope that was still live. That bit now lives
// on the stack entry, out of reach of recycling, so an explicitly requested
// pool has to survive a woven build.
//
// This asserts a config outcome, not pooling behavior, so it is deliberately a
// narrow test: what it catches is the gate being re-introduced. It only means
// anything in a build actually woven by orchestrion, because orchestrion.Enabled()
// is a build-time constant that plain `go test` always sees as false.
func TestSpanPoolEnabledUnderOrchestrion(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("pooling under a woven build is the whole subject of this test")
	}
	require.True(t, built.WithOrchestrion)

	t.Run("env var", func(t *testing.T) {
		t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "true")
		require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
		defer tracer.Stop()
		require.True(t, internalconfig.Get().SpanPoolEnabled(),
			"the span pool is no longer gated on Orchestrion: the GLS liveness bit moved off the span onto the stack entry")
	})

	t.Run("explicit option", func(t *testing.T) {
		require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(true)))
		defer tracer.Stop()
		require.True(t, internalconfig.Get().SpanPoolEnabled())
	})
}
