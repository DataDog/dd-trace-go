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
}

// newDynamicConfig creates a DynamicConfig with the given telemetry name, initial value,
// the origin that produced that initial value, and a comparator used to detect changes.
// The startupOrigin is used when RC resets the field back to its startup value.
func newDynamicConfig[T any](name string, val T, origin telemetry.Origin, equal func(T, T) bool) *DynamicConfig[T] {
	dc := &DynamicConfig[T]{cfgName: name, equal: equal}
	dc.setBaseline(val, origin)
	return dc
}

// setBaseline sets both the current value and the startup value for a field.
// The startup value is what the field reverts to when RC is reset or revoked.
func (dc *DynamicConfig[T]) setBaseline(val T, origin telemetry.Origin) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.current = val
	dc.startup = val
	dc.startupOrigin = origin
}

// Get returns the current value.
func (dc *DynamicConfig[T]) Get() T {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.current
}

// HandleRC processes a remote config update. If val is non-nil, the value is
// updated; if nil, the field is reset to its startup value.
// Reports the new value to telemetry when changed.
// Returns true if the value was changed.
func (dc *DynamicConfig[T]) HandleRC(val *T) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
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
	if changed {
		configtelemetry.Report(dc.cfgName, dc.current, origin)
	}
	return changed
}
