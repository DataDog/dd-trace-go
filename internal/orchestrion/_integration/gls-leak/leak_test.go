// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package main

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

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
// orchestrion#782: it runs the same owner/worker cross-goroutine workload as the
// runnable command (measureLeak) at a soak-sized record count and fails if the
// retained heap objects per record regress. Before the reclaim fix this leaked
// ~15 objects/record (millions retained over the run); the fix keeps it at ~0.
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

	r := measureLeak(200_000)
	// Generous bound: ~0/record with the fix, ~15/record without it. Well above
	// the GC/alloc noise floor and far below a regression.
	require.Lessf(t, r.perRecord, 1.0,
		"GLS span leak: %.3f retained heap objects/record (was ~15 before the reclaim "+
			"fix); the contextStack.Push reclaim in ddtrace/tracer/orchestrion.yml regressed",
		r.perRecord)
}
