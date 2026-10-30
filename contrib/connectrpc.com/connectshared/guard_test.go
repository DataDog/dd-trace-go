// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuard(t *testing.T) {
	for _, test := range []struct {
		name string
		// panicNil runs the case with GODEBUG=panicnil=1, with which recover reports a panic(nil) as
		// no panic, and runtime.Goexit alike.
		panicNil  bool
		op        func()
		want      callOutcome
		wantGuard Guard  // the guard that a function deferred by Run's caller sees
		wantStack string // a substring of the stack trace taken there
	}{
		{name: "return", op: func() {}},
		{
			name: "panic", op: func() { _ = panicSite() }, want: callOutcome{panicked: true, value: "panic site"},
			wantGuard: Guard{panicked: true, value: "panic site"}, wantStack: "connectshared.panicSite",
		},
		{name: "panic(nil)", panicNil: true, op: func() { panicWith(nil) }, want: callOutcome{panicked: true}, wantGuard: Guard{panicked: true}},
		{name: "runtime.Goexit", op: runtime.Goexit, want: callOutcome{goexit: true}},
		{name: "runtime.Goexit with panicnil", panicNil: true, op: runtime.Goexit, want: callOutcome{goexit: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.panicNil && !runsWithPanicNil(t) {
				return
			}
			var seen Guard
			var stack string
			o := outcomeOf(t, func() {
				var guard Guard
				defer func() {
					seen = guard
					stack = string(debug.Stack())
				}()
				guard.Run(test.op)
			})
			assert.Equal(t, test.want, o)
			assert.Equal(t, test.wantGuard, seen)
			assert.Contains(t, stack, test.wantStack)
		})
	}
}
