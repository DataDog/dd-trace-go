//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"math/bits"
	"reflect"
	"testing"
	"time"
	"unsafe"
)

// capFuzzParallelSeedsWait bounds the parallel-seed wait that the fuzz root
// lifecycle subtracts from the native duration of a testing.F.
//
// Go 1.23 through 1.27 measure fRunner's elapsed time with the
// QueryPerformanceCounter (highPrecisionTime in testing_windows.go), while
// completeParallelSubtestsWithState measures the wait with time.Since. On
// Windows, time.Since reads the interrupt-time clock, which advances only on
// timer interrupts. That clock can lead the counter by up to one interrupt
// period. Cap the subtraction at the elapsed counter time that fRunner will
// add, or the clock skew drives the compensated duration below zero.
func capFuzzParallelSeedsWait(f *testing.F, wait time.Duration) time.Duration {
	if wait <= 0 {
		return wait
	}
	layout := getTestingInternalsLayout()
	base := commonBaseForTest((*testing.T)(unsafe.Pointer(f)), layout)
	field := layout.common.start.unsafeField
	if base == nil || !field.available {
		return wait
	}
	value := reflect.NewAt(field.typ, fieldRawPtr(base, field)).Elem()
	if value.Type() == reflect.TypeFor[time.Time]() {
		// Go 1.28+ measures testing durations with time.Now on Windows too,
		// so the wait and fRunner's accumulator already share one clock.
		return wait
	}
	started := value.FieldByName("now")
	if !started.IsValid() || !started.CanAddr() || started.Kind() != reflect.Int64 {
		return wait
	}
	now, counterOK := performanceClockValue(performanceClockCounter)
	frequency, frequencyOK := performanceClockValue(performanceClockFrequency)
	if !counterOK || !frequencyOK || frequency <= 0 {
		return wait
	}
	startedCounter := started.Int()
	if now < startedCounter {
		return wait
	}
	// Match the conversion that testing applies to highPrecisionTime. Match
	// its truncation too, so the cap compares against exactly what fRunner
	// adds.
	hi, lo := bits.Mul64(uint64(now-startedCounter), uint64(time.Second)/uint64(time.Nanosecond))
	elapsed, _ := bits.Div64(hi, lo, uint64(frequency))
	if nativeElapsed := time.Duration(elapsed); wait > nativeElapsed {
		return nativeElapsed
	}
	return wait
}
