// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package crashtrackerstate tells the tracer and the profiler that the current
// process is about to become the crashtracker supervisor.
//
// With the crashtracker Orchestrion aspect, the supervisor and the application
// run the same binary. Orchestrion injects the tracer and profiler start calls
// into init functions of package main. These run before main, so without this
// package the supervisor would start a second tracer and a second profiler for
// the whole lifetime of the application.
//
// The crashtracker aspect injects a package variable in package main that calls
// [MarkSupervisorPending]. The tracer and profiler Start functions check
// [SupervisorPending] and call [Defer] instead of starting. The aspect also
// injects crashtracker.Start as the first statement of main, which calls
// [Release] when the process does not become the supervisor.
//
// If the supervision fails, the application continues in the same process. In
// that case, the tracer and profiler start at the crashtracker.Start call site
// instead of in init. In the application process, the flag is never set, and
// both start in init as usual.
//
// Without the aspect, nothing calls [MarkSupervisorPending] and every function
// of this package is a no-op.
package crashtrackerstate

import (
	"sync"
	"testing"
)

// inTestBinary reports whether this binary is a test binary. Tests replace it
// through IgnoreTestBinaryForTesting to exercise the resolver.
var inTestBinary = testing.Testing

// IgnoreTestBinaryForTesting makes MarkSupervisorPending behave as in a
// production binary, and returns a function that restores the check. It exists
// for tests only.
func IgnoreTestBinaryForTesting() (restore func()) {
	state.mu.Lock()
	defer state.mu.Unlock()
	previous := inTestBinary
	inTestBinary = func() bool { return false }
	return func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		inTestBinary = previous
	}
}

var state struct {
	mu       sync.Mutex
	resolve  func() bool
	pending  bool
	deferred []func()
}

// SetResolver registers the function that reports whether this process will
// become the crashtracker supervisor. The crashtracker package calls it in its
// init function. The crashtracker package owns the resolution because it owns
// the configuration and the marker of the application process.
func SetResolver(resolve func() bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.resolve = resolve
}

// MarkSupervisorPending records whether this process will become the
// crashtracker supervisor and returns the result. The crashtracker aspect
// calls it from a package variable initializer in package main, so the
// return value can initialize a blank variable.
//
// The Go specification initializes the package-level variables of a package
// before any of its init functions run
// (https://go.dev/ref/spec#Package_initialization). The tracer and profiler
// aspects inject their start calls into init functions of package main, so the
// flag is set before they run.
//
// The injected initializer runs in a test binary too, but that binary never
// runs main, so nothing ever releases the deferred starts. The inTestBinary
// guard is what keeps the flag false in a test binary.
func MarkSupervisorPending() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.pending = !inTestBinary() && state.resolve != nil && state.resolve()
	return state.pending
}

// SupervisorPending reports whether this process is about to become the
// crashtracker supervisor.
func SupervisorPending() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.pending
}

// Defer registers start to run if the supervision does not happen. Callers
// register it only while [SupervisorPending] is true. [Release] runs the
// registered functions in registration order.
func Defer(start func()) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.deferred = append(state.deferred, start)
}

// Release clears the pending flag and runs the deferred start functions once.
// The crashtracker package calls it when Start returns in a process that did
// not become the supervisor, for example after a supervision error.
func Release() {
	state.mu.Lock()
	state.pending = false
	deferred := state.deferred
	state.deferred = nil
	state.mu.Unlock()
	// Run outside the lock: a start function calls SupervisorPending.
	for _, start := range deferred {
		start()
	}
}
