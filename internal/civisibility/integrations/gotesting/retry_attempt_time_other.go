//go:build !windows

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
	*(*time.Time)(unsafe.Add(base, field.offset+layout.parallelNow.offset)) = time.Now()
}

func addRetryAttemptElapsed(base unsafe.Pointer, layout *testingInternalsLayout) {
	field := layout.common.start.unsafeField
	if base == nil || !field.available || !layout.parallelTimingOK {
		return
	}
	started := *(*time.Time)(unsafe.Add(base, field.offset+layout.parallelNow.offset))
	*fieldPtr[time.Duration](base, layout.common.duration) += time.Since(started)
}
