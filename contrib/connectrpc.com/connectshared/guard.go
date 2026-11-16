// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

// Guard runs the traced code of a function that defers FinishOnReturn or FinishOnOpenPanic, and
// tells them whether that code panicked: recover reports nil both for runtime.Goexit and, with
// GODEBUG=panicnil=1, for panic(nil), so a deferred function cannot tell them apart itself. The
// zero value is ready to use, for one Run.
type Guard struct {
	panicked bool
	value    any
}

// Run calls op. If op panics, Run records the panic and re-raises it: a non-nil value while it is
// in flight, so that the functions deferred by Run's caller, and the stack traces taken there,
// still show where op panicked; a nil value once the stack has unwound. If op calls runtime.Goexit,
// Run does not return and records no panic.
func (g *Guard) Run(op func()) {
	// Not recoverPanic(op, g.reraise): the frames between op and Run's caller are in the stacks that
	// spans record for errors within op, and each costs allocations.
	returned := false
	func() {
		defer func() {
			if returned {
				return
			}
			if value := recover(); value != nil {
				g.reraise(value)
			}
		}()
		op()
		returned = true
	}()
	if !returned {
		// recover stopped a panic(nil): runtime.Goexit would not have let the function above return.
		g.reraise(nil)
	}
}

func (g *Guard) reraise(value any) {
	g.panicked, g.value = true, value
	panic(value)
}

// recoverPanic calls fn and reports whether it panicked, and with which value. onPanic runs with
// the value before recoverPanic returns: while the panic is in flight, so that a stack trace taken
// there still shows where fn panicked, unless the value is nil. Only a recovered panic lets the
// function that deferred recover return, which tells a panic(nil) with GODEBUG=panicnil=1 apart
// from runtime.Goexit; recoverPanic lets a Goexit continue.
func recoverPanic(fn func(), onPanic func(value any)) (panicked bool, value any) {
	returned := false
	func() {
		defer func() {
			if returned {
				return
			}
			if value = recover(); value != nil {
				panicked = true
				onPanic(value)
			}
		}()
		fn()
		returned = true
	}()
	if !returned && !panicked {
		panicked = true
		onPanic(nil)
	}
	return panicked, value
}
