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

// TestSpanPoolDisabledUnderOrchestrion is the woven counterpart to
// ddtrace/tracer's TestSpanPoolOrchestrionGateWiring: orchestrion.Enabled() is
// a build-time constant that plain `go test` always sees as false, so the
// on-branch of the gate (ddtrace/tracer/option.go's newConfig, which forces
// the experimental span pool off when Orchestrion's GLS weave is active — see
// orchestrion#782) can only be exercised in a build actually woven by
// orchestrion. This package already runs woven in the orchestrion CI lane, so
// it is the natural home for that assertion.
func TestSpanPoolDisabledUnderOrchestrion(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("the span pool gate only trips in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	t.Run("env var", func(t *testing.T) {
		t.Setenv("DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED", "true")
		require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
		defer tracer.Stop()
		require.False(t, internalconfig.Get().SpanPoolEnabled(),
			"span pool must be force-disabled under orchestrion (orchestrion#782)")
	})

	t.Run("explicit option", func(t *testing.T) {
		require.NoError(t, tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(true)))
		defer tracer.Stop()
		require.False(t, internalconfig.Get().SpanPoolEnabled())
	})
}
