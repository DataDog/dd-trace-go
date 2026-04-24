// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"reflect"
	"runtime"
)

// adjacentLiteralRuntimeFuncs returns runtime functions for two adjacent one-line closures.
//
// The consecutive closure body lines are the regression invariant for exact literal matching.
//
//go:noinline
func adjacentLiteralRuntimeFuncs() (*runtime.Func, *runtime.Func) {
	first := func() { runtime.Gosched() }
	second := func() { runtime.Gosched() }
	return runtime.FuncForPC(reflect.ValueOf(first).Pointer()), runtime.FuncForPC(reflect.ValueOf(second).Pointer())
}
