//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Go 1.23 through 1.27 measure testing durations with the
// QueryPerformanceCounter (highPrecisionTime in testing_windows.go). The
// helpers below read the same counter, so native duration accounting stays on
// the clock that testing uses. Go 1.28 drops highPrecisionTime and measures
// testing durations with time.Now on every platform.
// TODO: drop these helpers, and the layout checks that select them, once Go
// 1.27 falls out of support.
var (
	performanceClockKernel32  = windows.NewLazySystemDLL("kernel32.dll")
	performanceClockCounter   = performanceClockKernel32.NewProc("QueryPerformanceCounter")
	performanceClockFrequency = performanceClockKernel32.NewProc("QueryPerformanceFrequency")
)

// performanceClockValue reads one counter or frequency value.
func performanceClockValue(proc *windows.LazyProc) (int64, bool) {
	var value int64
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&value)))
	return value, ok != 0
}
