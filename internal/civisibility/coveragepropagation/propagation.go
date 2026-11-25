// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package coveragepropagation lets the per-test line coverage of Test
// Optimization follow a request to a server in the same process, the way trace
// context does. The client integration reports the request it sent under the
// span ID it injected, and the server integration that extracts that span ID
// runs the handler in the coverage context of the goroutine that sent it.
package coveragepropagation

import "sync/atomic"

// Propagator moves goroutines between coverage contexts. The line coverage
// collector installs one while it runs.
type Propagator interface {
	// Sent records that the calling goroutine sent a request that carries the
	// span context of spanID. The returned function forgets the request.
	Sent(spanID uint64) (forget func())
	// Received moves the calling goroutine to the coverage context of the
	// goroutine that sent spanID, when a goroutine of this process sent it. The
	// returned function moves it back.
	Received(spanID uint64) (restore func())
}

type holder struct{ propagator Propagator }

var installed atomic.Pointer[holder]

func noop() {}

// Set installs p, or removes the installed propagator when p is nil.
func Set(p Propagator) {
	if p == nil {
		installed.Store(nil)
		return
	}
	installed.Store(&holder{propagator: p})
}

// Sent calls Sent of the installed propagator, if any.
func Sent(spanID uint64) (forget func()) {
	if h := installed.Load(); h != nil {
		return h.propagator.Sent(spanID)
	}
	return noop
}

// Received calls Received of the installed propagator, if any.
func Received(spanID uint64) (restore func()) {
	if h := installed.Load(); h != nil {
		return h.propagator.Received(spanID)
	}
	return noop
}
