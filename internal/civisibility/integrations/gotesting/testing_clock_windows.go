//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"math/bits"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	testingKernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	testingQueryPerformanceCounter   = testingKernel32.NewProc("QueryPerformanceCounter")
	testingQueryPerformanceFrequency = testingKernel32.NewProc("QueryPerformanceFrequency")
	testingClockFrequencyOnce        sync.Once
	testingClockFrequencyValue       int64
)

func testingClockNow() (int64, bool) {
	return testingPerformanceValue(testingQueryPerformanceCounter)
}

func testingClockFrequency() int64 {
	testingClockFrequencyOnce.Do(func() {
		frequency, ok := testingPerformanceValue(testingQueryPerformanceFrequency)
		if ok && frequency > 0 {
			testingClockFrequencyValue = frequency
		}
	})
	return testingClockFrequencyValue
}

func testingPerformanceValue(proc *windows.LazyProc) (int64, bool) {
	var value int64
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&value)))
	return value, ok != 0
}

func testingClockDuration(delta, frequency int64) (time.Duration, bool) {
	if delta < 0 || frequency <= 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(uint64(delta), uint64(time.Second)/uint64(time.Nanosecond))
	if hi >= uint64(frequency) {
		return 0, false
	}
	nanos, _ := bits.Div64(hi, lo, uint64(frequency))
	if nanos > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return time.Duration(nanos), true
}
