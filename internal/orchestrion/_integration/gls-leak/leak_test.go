// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package main

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/glsleak"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
)

// orchestrionEnabled is flipped to true by orchestrion at build time via the
// //dd:orchestrion-enabled directive, so the gate below only asserts on builds
// where the GLS feature actually exists.
//
//dd:orchestrion-enabled
const orchestrionEnabled = false

func TestBuiltWithOrchestrion(t *testing.T) {
	require.Equal(t, built.WithOrchestrion, orchestrionEnabled)
}

// TestGLSNoHeapLeak is the comprehensive, end-to-end regression gate for
// orchestrion#782: it runs the shared owner/worker cross-goroutine workload
// (glsleak.MeasureLeak) at a soak-sized record count and fails if the retained
// heap objects per record regress. Without the reclaim fix the worker's GLS stack
// grows by one span per record (retention proportional to the record count); the
// fix keeps it flat.
//
// This is the orchestrion-native home for the korECM repro: internal/apps is
// built without orchestrion, so the leak (which only exists under orchestrion)
// cannot be exercised there — only this _integration lane runs woven.
func TestGLSNoHeapLeak(t *testing.T) {
	if !orchestrionEnabled {
		t.Skip("GLS only exists in orchestrion builds")
	}
	require.True(t, built.WithOrchestrion)

	require.NoError(t, tracer.Start(tracer.WithLogStartup(false)))
	defer tracer.Stop()

	r := glsleak.MeasureLeak(200_000)
	require.Lessf(t, r.PerRecord, glsleak.MaxRetainedObjectsPerRecord,
		"GLS span leak: %.3f retained heap objects/record (want flat ~0; the leak grows "+
			"one span per record) — the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		r.PerRecord)
}
