// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"math"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// equalFloat compares two float64 values, treating NaN as equal to NaN.
// IEEE 754 defines NaN != NaN, but for config purposes two NaN values
// represent the same "unset" sentinel.
func equalFloat(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// equal compares two scalar values using Go's == operator. Use for bool, int,
// string, and other comparable types where reference equality is sufficient.
// For float64, prefer equalFloat to handle NaN.
func equal[T comparable](a, b T) bool {
	return a == b
}

// equalSlice compares two slices element-wise (order-sensitive).
func equalSlice[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// equalMap compares two maps of comparable keys and any values, comparing
// values with ==. It is used as the change detector for map-valued dynamic
// configs (e.g. global tags). Note: == panics if a value is non-comparable
// (e.g. a slice); callers must only store comparable values.
func equalMap[K comparable](x, y map[K]any) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if yv, ok := y[k]; !ok || yv != v {
			return false
		}
	}
	return true
}

// DynamicConfig is a thread-safe, RC-aware value store for a single configuration field.
// It tracks both the current value and the startup baseline (for RC reset).
// Consumers read via Get().
type DynamicConfig[T any] struct {
	mu            sync.RWMutex
	current       T
	startup       T
	cfgName       string
	startupOrigin telemetry.Origin // used on RC reset; updated by setBaseline
	equal         func(T, T) bool  // compares values to avoid unnecessary updates
	// executes any config-specific operations to propagate the update properly, returns whether the update was applied
	apply func(T) bool
}

// newDynamicConfig creates a DynamicConfig with the given telemetry name, initial value,
// the origin that produced that initial value, a comparator used to detect changes,
// and an optional apply callback invoked when the value changes (pass nil if not needed).
// The startupOrigin is used when RC resets the field back to its startup value.
func newDynamicConfig[T any](name string, val T, origin telemetry.Origin, equal func(T, T) bool, apply func(T) bool) *DynamicConfig[T] {
	dc := &DynamicConfig[T]{cfgName: name, equal: equal, apply: apply}
	dc.setBaseline(val, origin)
	return dc
}

// setBaseline sets both the current value and the startup value for a field.
// The startup value is what the field reverts to when RC is reset or revoked.
// If an apply callback was registered and the new value differs from the current
// value, the callback fires after the lock is released.
func (dc *DynamicConfig[T]) setBaseline(val T, origin telemetry.Origin) {
	dc.mu.Lock()
	changed := !dc.equal(dc.current, val)
	dc.current = val
	dc.startup = val
	dc.startupOrigin = origin
	apply := dc.apply
	dc.mu.Unlock()
	if changed && apply != nil {
		apply(val)
	}
}

// Get returns the current value.
func (dc *DynamicConfig[T]) Get() T {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.current
}

// Baseline returns the startup value and its origin atomically.
func (dc *DynamicConfig[T]) Baseline() (T, telemetry.Origin) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.startup, dc.startupOrigin
}

// HandleRC processes a remote config update. If val is non-nil, the value is
// updated; if nil, the field is reset to its startup value.
// Reports the new value to telemetry when changed and invokes the apply
// callback (if registered) outside the lock.
// Returns true if the value was changed.
func (dc *DynamicConfig[T]) HandleRC(val *T) bool {
	dc.mu.Lock()
	var changed bool
	var origin telemetry.Origin
	if val != nil {
		if !dc.equal(dc.current, *val) {
			dc.current = *val
			changed = true
		}
		origin = telemetry.OriginRemoteConfig
	} else {
		if !dc.equal(dc.current, dc.startup) {
			dc.current = dc.startup
			changed = true
		}
		origin = dc.startupOrigin
	}
	newVal := dc.current
	apply := dc.apply
	if changed {
		configtelemetry.Report(dc.cfgName, newVal, origin)
	}
	dc.mu.Unlock()
	if changed && apply != nil {
		apply(newVal)
	}
	return changed
}
