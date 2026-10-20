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

// The testing package measures durations with the QueryPerformanceCounter
// (highPrecisionTime, testing_windows.go). The shared helpers below read the
// same counter, so native duration accounting stays on the clock that testing
// uses.
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
