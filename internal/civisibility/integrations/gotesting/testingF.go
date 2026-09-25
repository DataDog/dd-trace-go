// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"reflect"
	"runtime"
	"testing"
	_ "unsafe"
)

// F adapts testing.F methods that need CI Visibility instrumentation.
type F testing.F

// GetFuzz returns the CI Visibility adapter for f.
func GetFuzz(f *testing.F) *F {
	return (*F)(f)
}

// Fuzz runs ff as the fuzz target and reports each seed corpus execution as a
// native test event. Active fuzzing mutations remain owned by testing.F.
func (ddf *F) Fuzz(ff any) {
	f := (*testing.F)(ddf)
	if isTestingBuiltWithOrchestrion() {
		// The woven testing.F.Fuzz body instruments this argument. Avoid wrapping
		// it twice when callers use the manual adapter in an Orchestrion binary.
		f.Fuzz(ff)
		return
	}
	f.Fuzz(instrumentTestingFuzzFunc(ff))
}

// instrumentTestingFuzzFunc preserves the callback's exact concrete type,
// which testing.F.Fuzz validates through reflection.
//
//go:linkname instrumentTestingFuzzFunc
func instrumentTestingFuzzFunc(ff any) any {
	release, ok := acquireOrchestrionTestingHook()
	if !ok {
		return ff
	}
	defer release()
	if isProcessRetryChild() {
		return ff
	}
	if testingFuzzingActive() {
		// Generated fuzzing mutations are not JUnit test cases. The root fuzz
		// target is still reported by its testing.M descriptor wrapper.
		return ff
	}
	if !isCiVisibilityEnabled() || !testing.Testing() || ff == nil {
		return ff
	}

	fn := reflect.ValueOf(ff)
	fnType := fn.Type()
	testingTPtr := reflect.TypeFor[*testing.T]()
	if fn.Kind() != reflect.Func || fnType.NumIn() == 0 || fnType.In(0) != testingTPtr || fnType.NumOut() != 0 {
		// Let testing.F.Fuzz produce its native validation error unchanged.
		return ff
	}

	sourceFunc := runtime.FuncForPC(fn.Pointer())
	return reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		t := args[0].Interface().(*testing.T)
		seedBody := func(currentT *testing.T) {
			args[0] = reflect.ValueOf(currentT)
			fn.Call(args)
		}
		instrumentTestingTFuncWithSource(seedBody, sourceFunc, false)(t)
		return nil
	}).Interface()
}

func testingFuzzingActive() bool {
	fuzz := flag.Lookup("test.fuzz")
	return fuzz != nil && fuzz.Value.String() != ""
}

func testingFuzzWorkerActive() bool {
	worker := flag.Lookup("test.fuzzworker")
	return worker != nil && worker.Value.String() == "true"
}
