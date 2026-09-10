// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"reflect"
	"runtime"
)

// func1 provides a real generated-looking declaration for resolver shadowing tests.
//
//dd:test.unskippable
//go:noinline
func func1() {
	_ = 1
	_ = 2
}

// func1ShadowClosureRuntimeFunc returns the runtime function for a closure whose short name is func1.
//
//go:noinline
func func1ShadowClosureRuntimeFunc() *runtime.Func {
	literal := func() {
		runtime.Gosched()
	}
	return runtime.FuncForPC(reflect.ValueOf(literal).Pointer())
}
