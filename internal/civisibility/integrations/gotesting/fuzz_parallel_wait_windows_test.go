//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func setFuzzPerformanceStart(t *testing.T, f *testing.F, counter int64) {
	t.Helper()
	layout := getTestingInternalsLayout()
	base := commonBaseForTest((*testing.T)(unsafe.Pointer(f)), layout)
	field := layout.common.start.unsafeField
	require.True(t, field.available)
	started := reflect.NewAt(field.typ, fieldRawPtr(base, field)).Elem().FieldByName("now")
	require.True(t, started.IsValid() && started.CanAddr() && started.Kind() == reflect.Int64)
	// The "now" field is unexported, so reflect marks its value read-only.
	// Re-wrap the field address before writing, like retry_attempt_time_windows.go
	// does when it initializes the same field.
	reflect.NewAt(started.Type(), unsafe.Pointer(started.UnsafeAddr())).Elem().SetInt(counter)
}

func TestCompleteFuzzParallelSeedsCapsWaitByNativeElapsed(t *testing.T) {
	if getTestingInternalsLayout().common.start.typ == reflect.TypeFor[time.Time]() {
		t.Skip("Go 1.28+ keeps testing.common.start as time.Time on Windows; the wait and fRunner share one clock")
	}
	synctest.Test(t, func(t *testing.T) {
		f := &testing.F{}
		fields := getTestPrivateFields((*testing.T)(unsafe.Pointer(f)))
		*fields.barrier = make(chan bool)
		seed := &testing.T{}
		seedFields := getTestPrivateFields(seed)
		*seedFields.signal = make(chan bool)
		*fields.sub = append(*fields.sub, seed)
		go func() {
			<-*fields.barrier
			time.Sleep(time.Hour) // Virtual time: instant in real time, but far past the counter window.
			*seedFields.signal <- true
		}()
		counter, ok := performanceClockValue(performanceClockCounter)
		require.True(t, ok)
		setFuzzPerformanceStart(t, f, counter)
		setFuzzNativeField(t, f, "duration", 20*time.Millisecond)

		completeFuzzParallelSeeds(f)

		event := fuzzTestEvent{native: f}
		_, _, duration, durationRead := event.nativeResult(true)
		require.True(t, durationRead)
		// Without the cap, the subtraction consumes the whole one-hour virtual wait
		// and drives the duration below zero. The cap bounds the subtraction at the
		// real counter window, so the 20ms budget keeps nearly all of its length:
		// above 10ms even when the drained window stretches to 10ms of real
		// scheduling, and below 20ms because the real counter window is never zero.
		require.Less(t, duration, 20*time.Millisecond, "the capped wait must exclude at least the real drained window")
		require.Greater(t, duration, 10*time.Millisecond, "the cap must bound the subtraction to the real counter window, not the virtual wait")
		require.Empty(t, *fields.sub)
	})
}
