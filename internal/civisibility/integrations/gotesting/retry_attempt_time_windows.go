//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"math/bits"
	"reflect"
	"time"
	"unsafe"
)

func initializeRetryAttemptStart(base unsafe.Pointer, field unsafeField) {
	if base == nil || !field.available {
		return
	}
	counter, ok := performanceClockValue(performanceClockCounter)
	if !ok {
		return
	}
	value := reflect.NewAt(field.typ, fieldRawPtr(base, field)).Elem()
	now := value.FieldByName("now")
	if now.IsValid() && now.CanAddr() && now.Kind() == reflect.Int64 {
		reflect.NewAt(now.Type(), unsafe.Pointer(now.UnsafeAddr())).Elem().SetInt(counter)
	}
}

func addRetryAttemptElapsed(base unsafe.Pointer, layout *testingInternalsLayout) {
	field := layout.common.start.unsafeField
	if base == nil || !field.available {
		return
	}
	now, counterOK := performanceClockValue(performanceClockCounter)
	frequency, frequencyOK := performanceClockValue(performanceClockFrequency)
	if !counterOK || !frequencyOK || frequency <= 0 {
		return
	}
	value := reflect.NewAt(field.typ, fieldRawPtr(base, field)).Elem()
	startedField := value.FieldByName("now")
	if !startedField.IsValid() || !startedField.CanAddr() || startedField.Kind() != reflect.Int64 {
		return
	}
	started := reflect.NewAt(startedField.Type(), unsafe.Pointer(startedField.UnsafeAddr())).Elem().Int()
	if now <= started {
		return
	}
	hi, lo := bits.Mul64(uint64(now-started), uint64(time.Second)/uint64(time.Nanosecond))
	elapsedNanoseconds, _ := bits.Div64(hi, lo, uint64(frequency))
	elapsed := time.Duration(elapsedNanoseconds)
	*fieldPtr[time.Duration](base, layout.common.duration) += elapsed
}
