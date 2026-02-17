// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"runtime"
	"strconv"
	"sync"
)

// MockGLS sets up a mock GLS for testing and returns a cleanup function that
// restores the original state. It enables orchestrion and configures a fresh
// contextStack accessible via the standard getDDGLS/setDDGLS functions.
//
// This is intended for use by tests in packages that depend on orchestrion
// (e.g., internal, ddtrace/tracer). Follows the same pattern as
// telemetry.MockClient in internal/telemetry/globalclient.go.
//
// Tests using MockGLS must NOT use t.Parallel, as it mutates package-level
// variables without synchronization.
func MockGLS() func() {
	prevGetDDGLS := getDDGLS
	prevSetDDGLS := setDDGLS
	prevEnabled := enabled

	tmp := contextStack(make(map[any][]any))
	var glsValue any = &tmp
	getDDGLS = func() any { return glsValue }
	setDDGLS = func(a any) { glsValue = a }
	enabled = true

	return func() {
		getDDGLS = prevGetDDGLS
		setDDGLS = prevSetDDGLS
		enabled = prevEnabled
	}
}

// goroutineID extracts the current goroutine ID from runtime.Stack output.
// This is an intentionally test-only helper — parsing goroutine IDs is not
// safe for production use but is acceptable for test mocks.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// runtime.Stack output starts with "goroutine <id> ["
	s := buf[len("goroutine "):n]
	for i, b := range s {
		if b == ' ' {
			s = s[:i]
			break
		}
	}
	id, _ := strconv.ParseUint(string(s), 10, 64)
	return id
}

// MockGLSPerGoroutine sets up per-goroutine GLS isolation for testing.
// Unlike MockGLS which uses a single shared GLS, this mock uses goroutine IDs
// to give each goroutine its own independent contextStack, simulating the real
// orchestrion runtime behavior where each runtime.g has its own GLS slot.
//
// This is required for tests that spawn goroutines and need to verify
// cross-goroutine GLS behavior (e.g., GLSPopFunc no-op on wrong goroutine).
//
// Tests using MockGLSPerGoroutine must NOT use t.Parallel, as it mutates
// package-level variables without synchronization.
func MockGLSPerGoroutine() func() {
	prevGetDDGLS := getDDGLS
	prevSetDDGLS := setDDGLS
	prevEnabled := enabled

	var stacks sync.Map // goroutineID → any
	getDDGLS = func() any {
		val, _ := stacks.Load(goroutineID())
		return val
	}
	setDDGLS = func(a any) {
		stacks.Store(goroutineID(), a)
	}
	enabled = true

	return func() {
		getDDGLS = prevGetDDGLS
		setDDGLS = prevSetDDGLS
		enabled = prevEnabled
	}
}
