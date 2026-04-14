// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"math"
	"reflect"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// equal reports whether a and b are deeply equal, with a special case
// for float64 NaN (reflect.DeepEqual treats NaN != NaN per IEEE 754,
// but for config purposes two NaN values are the same "unset" sentinel).
func equal[T any](a, b T) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	if fa, ok := any(a).(float64); ok {
		fb, _ := any(b).(float64)
		return math.IsNaN(fa) && math.IsNaN(fb)
	}
	return false
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
}

// newDynamicConfig creates a DynamicConfig with the given telemetry name, initial value,
// and the origin that produced that initial value. The startupOrigin is used when RC
// resets the field back to its startup value.
func newDynamicConfig[T any](name string, val T, origin telemetry.Origin) *DynamicConfig[T] {
	dc := &DynamicConfig[T]{cfgName: name}
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
	var changed bool
	var origin telemetry.Origin
	if val != nil {
		if !equal(dc.current, *val) {
			dc.current = *val
			changed = true
		}
		origin = telemetry.OriginRemoteConfig
	} else {
		if !equal(dc.current, dc.startup) {
			dc.current = dc.startup
			changed = true
		}
		origin = dc.startupOrigin
	}
	dc.mu.Unlock()
	if changed {
		configtelemetry.Report(dc.cfgName, dc.current, origin)
	}
	return changed
}
