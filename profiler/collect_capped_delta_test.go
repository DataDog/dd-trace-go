// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/profiler/internal/fastdelta"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pprofutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCappedSampleCountSurvivesDelta verifies that the sample count in the
// delta output does not exceed the cap set on the raw pprof input.
func TestCappedSampleCountSurvivesDelta(t *testing.T) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()

	allocUniqueStacks(500)
	runtime.GC()
	runtime.GC()

	total, _ := runtime.MemProfile(nil, true)
	require.Greater(t, total, 20, "need more than 20 runtime entries to test the cap")

	const cap = 10
	raw, err := buildCappedHeapPprof(cap)
	require.NoError(t, err)

	rawSamples := countSamplesInRaw(t, raw)
	assert.LessOrEqual(t, rawSamples, cap, "raw pprof must not exceed cap")
	t.Logf("runtime entries: %d, cap: %d, raw samples: %d", total, cap, rawSamples)

	// Run through fastdelta (first call: delta vs empty = full profile).
	dc := fastdelta.NewDeltaComputer(
		pprofutils.ValueType{Type: "alloc_objects", Unit: "count"},
		pprofutils.ValueType{Type: "alloc_space", Unit: "bytes"},
	)
	var deltaBuf bytes.Buffer
	require.NoError(t, dc.Delta(raw, &deltaBuf))

	deltaBytes := deltaBuf.Bytes()
	require.NotEmpty(t, deltaBytes, "delta output must be non-empty")

	deltaSamples := countSamplesInRaw(t, deltaBytes)
	t.Logf("delta output samples: %d", deltaSamples)
	assert.LessOrEqual(t, deltaSamples, cap, "delta output must not exceed cap")
}
