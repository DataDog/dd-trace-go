//go:build !go1.19

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package atomic

import stdatomic "sync/atomic"

// An Int64 is an atomic int64. The zero value is zero.
//
// This is copy & pasted from the go1.19 sync.atomic package while we're still
// supporting go1.18. The Go License applies:
//
// https://github.com/golang/go/blob/master/LICENSE
type Int64 struct{ v int64 }

// Load atomically loads and returns the value stored in x.
func (x *Int64) Load() int64 { return stdatomic.LoadInt64(&x.v) }

// Store atomically stores val into x.
func (x *Int64) Store(val int64) { stdatomic.StoreInt64(&x.v, val) }

// Swap atomically stores new into x and returns the previous value.
func (x *Int64) Swap(new int64) (old int64) { return stdatomic.SwapInt64(&x.v, new) }

// CompareAndSwap executes the compare-and-swap operation for x.
func (x *Int64) CompareAndSwap(old, new int64) (swapped bool) {
	return stdatomic.CompareAndSwapInt64(&x.v, old, new)
}

// Add atomically adds delta to x and returns the new value.
func (x *Int64) Add(delta int64) (new int64) { return stdatomic.AddInt64(&x.v, delta) }

// Value is an alias for atomic.Value.
type Value = stdatomic.Value
