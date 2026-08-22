//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	retryAttemptKernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	retryAttemptQueryPerformanceCounter   = retryAttemptKernel32.NewProc("QueryPerformanceCounter")
	retryAttemptQueryPerformanceFrequency = retryAttemptKernel32.NewProc("QueryPerformanceFrequency")
)

func retryAttemptPerformanceValue(proc *windows.LazyProc) (int64, bool) {
	var value int64
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&value)))
	return value, ok != 0
}

func initializeRetryAttemptStart(base unsafe.Pointer, field unsafeField) {
	layout := getTestingInternalsLayout()
	if base == nil || !field.available || layout == nil || !layout.parallelTimingOK {
		return
	}
	counter, ok := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
	if !ok {
		return
	}
	*(*int64)(unsafe.Add(base, field.offset+layout.parallelNow.offset)) = counter
}

func addRetryAttemptElapsed(base unsafe.Pointer, layout *testingInternalsLayout) {
	field := layout.common.start.unsafeField
	if base == nil || !field.available || !layout.parallelTimingOK {
		return
	}
	now, counterOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
	frequency := getParallelTimingFrequency()
	if !counterOK || frequency <= 0 {
		return
	}
	started := *(*int64)(unsafe.Add(base, field.offset+layout.parallelNow.offset))
	if now <= started {
		return
	}
	elapsed, ok := parallelCounterDuration(now-started, frequency)
	if !ok {
		return
	}
	*fieldPtr[time.Duration](base, layout.common.duration) += elapsed
}
