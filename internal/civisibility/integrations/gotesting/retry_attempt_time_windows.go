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
	if base == nil || !field.available {
		return
	}
	counter, ok := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
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
	now, counterOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
	frequency, frequencyOK := retryAttemptPerformanceValue(retryAttemptQueryPerformanceFrequency)
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
