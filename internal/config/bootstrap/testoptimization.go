// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bootstrap

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
)

const (
	testOptimizationManifestKey = "DD_TEST_OPTIMIZATION_MANIFEST_FILE"
	testOptimizationPayloadsKey = "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES"
)

// TestOptimizationSnapshot contains the process-wide Bazel transport settings
// needed by telemetry before the full configuration package can be linked.
type TestOptimizationSnapshot struct {
	ManifestFile    string
	PayloadsInFiles bool

	ManifestRaw     string
	ManifestPresent bool
	PayloadsRaw     string
	PayloadsPresent bool
	PayloadsError   error
}

var (
	testOptimizationStatePointer atomic.Pointer[testOptimizationState]
)

type testOptimizationState struct {
	once     sync.Once
	snapshot TestOptimizationSnapshot
	reported atomic.Bool
}

// TestOptimization returns the process-wide bootstrap snapshot.
func TestOptimization() TestOptimizationSnapshot {
	state := loadTestOptimizationState()
	state.once.Do(func() {
		state.snapshot = resolveTestOptimization()
	})
	return state.snapshot
}

// ClaimTestOptimizationTelemetry returns the snapshot and whether the caller
// owns its one registry telemetry projection.
func ClaimTestOptimizationTelemetry() (TestOptimizationSnapshot, bool) {
	state := loadTestOptimizationState()
	state.once.Do(func() {
		state.snapshot = resolveTestOptimization()
	})
	return state.snapshot, state.reported.CompareAndSwap(false, true)
}

func loadTestOptimizationState() *testOptimizationState {
	state := testOptimizationStatePointer.Load()
	if state != nil {
		return state
	}
	state = new(testOptimizationState)
	if testOptimizationStatePointer.CompareAndSwap(nil, state) {
		return state
	}
	return testOptimizationStatePointer.Load()
}

func resolveTestOptimization() TestOptimizationSnapshot {
	snapshot := TestOptimizationSnapshot{}
	snapshot.ManifestRaw = env.Get(testOptimizationManifestKey)
	snapshot.ManifestPresent = snapshot.ManifestRaw != ""
	snapshot.ManifestFile = strings.TrimSpace(snapshot.ManifestRaw)

	snapshot.PayloadsRaw, snapshot.PayloadsPresent = env.Lookup(testOptimizationPayloadsKey)
	if snapshot.PayloadsPresent {
		snapshot.PayloadsInFiles, snapshot.PayloadsError = strconv.ParseBool(strings.TrimSpace(snapshot.PayloadsRaw))
	}
	return snapshot
}

// ResetTestOptimizationForTesting clears the cached bootstrap snapshot.
func ResetTestOptimizationForTesting() {
	testOptimizationStatePointer.Store(new(testOptimizationState))
}
