//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"time"
	"unsafe"
)

func initializeRetryAttemptStart(base unsafe.Pointer, field unsafeField) {
	layout := getTestingInternalsLayout()
	if base == nil || !field.available || layout == nil || !layout.parallelTimingOK {
		return
	}
	counter, ok := testingClockNow()
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
	now, counterOK := testingClockNow()
	frequency := testingClockFrequency()
	if !counterOK || frequency <= 0 {
		return
	}
	started := *(*int64)(unsafe.Add(base, field.offset+layout.parallelNow.offset))
	if now <= started {
		return
	}
	elapsed, ok := testingClockDuration(now-started, frequency)
	if !ok {
		return
	}
	*fieldPtr[time.Duration](base, layout.common.duration) += elapsed
}
