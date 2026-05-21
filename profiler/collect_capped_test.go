// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"runtime"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/profiler/internal/fastdelta"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pproflite"
	"github.com/DataDog/dd-trace-go/v2/profiler/internal/pprofutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var heapSink [][]byte

// allocUniqueStacks generates n allocations from distinct call sites.
//
//go:noinline
func allocUniqueStacks(n int) {
	for i := range n {
		heapSink = append(heapSink, makeAlloc(i))
	}
}

//go:noinline
func makeAlloc(n int) []byte { return make([]byte, 1024+n) }

func TestBuildCappedHeapPprof(t *testing.T) {
	old := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = old }()

	allocUniqueStacks(200)
	runtime.GC()
	runtime.GC()

	total, _ := runtime.MemProfile(nil, true)
	require.Greater(t, total, 0, "expected heap profile entries")

	cap := 10
	raw, err := buildCappedHeapPprof(cap)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "capped pprof must be non-empty")

	// Parse it with fastdelta to confirm it's valid pprof protobuf.
	dc := fastdelta.NewDeltaComputer(
		pprofutils.ValueType{Type: "alloc_objects", Unit: "count"},
		pprofutils.ValueType{Type: "alloc_space", Unit: "bytes"},
	)
	var buf nopWriter
	err = dc.Delta(raw, &buf)
	require.NoError(t, err, "fastdelta must accept our capped pprof as valid")
	assert.NotEmpty(t, buf.n, "delta output must be non-empty")

	// Verify the cap: parse sample count from raw pprof.
	// Verify the cap on the RAW bytes (before delta): this is what determines
	// how many stacks go through symbol resolution. This is the key invariant.
	samples := countSamplesInRaw(t, raw)
	assert.LessOrEqual(t, samples, cap, "raw samples must not exceed cap")
	assert.Greater(t, samples, 0, "must have at least one sample")
	t.Logf("runtime entries: %d, cap: %d, raw serialized: %d", total, cap, samples)
}

func TestBuildCappedMutexPprof(t *testing.T) {
	old := runtime.SetMutexProfileFraction(1)
	defer runtime.SetMutexProfileFraction(old)

	// Generate some mutex contention.
	var mu sync.Mutex
	for range 50 {
		mu.Lock()
		mu.Unlock()
	}

	cap := 5
	raw, err := buildCappedMutexPprof(cap)
	require.NoError(t, err)
	// May be empty if no contention was recorded; just verify no error and valid format.
	if len(raw) > 0 {
		dc := fastdelta.NewDeltaComputer(
			pprofutils.ValueType{Type: "contentions", Unit: "count"},
			pprofutils.ValueType{Type: "delay", Unit: "nanoseconds"},
		)
		var buf nopWriter
		err = dc.Delta(raw, &buf)
		assert.NoError(t, err)
	}
	t.Logf("raw mutex pprof: %d bytes", len(raw))
}

type nopWriter struct{ n int }

func (w *nopWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

// countSamplesInRaw counts Sample records in a raw pprof protobuf
// using the pproflite decoder (same parser fastdelta uses).
func countSamplesInRaw(t *testing.T, data []byte) int {
	t.Helper()
	dec := pproflite.NewDecoder(data)
	count := 0
	err := dec.FieldEach(func(f pproflite.Field) error {
		if _, ok := f.(*pproflite.Sample); ok {
			count++
		}
		return nil
	}, pproflite.SampleDecoder)
	require.NoError(t, err)
	return count
}
