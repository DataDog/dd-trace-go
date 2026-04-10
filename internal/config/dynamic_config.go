// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"reflect"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// DynamicConfig is a thread-safe, RC-aware value store for a single configuration field.
// It tracks both the current value and the startup baseline (for RC reset).
// Consumers read via Get().
type DynamicConfig[T any] struct {
	mu      sync.RWMutex
	current T
	startup T
	cfgName string
	origin  telemetry.Origin
}

// newDynamicConfig creates a DynamicConfig with the given telemetry name and initial value.
// Both current and startup are set to val; origin defaults to OriginDefault.
func newDynamicConfig[T any](name string, val T) *DynamicConfig[T] {
	return &DynamicConfig[T]{
		cfgName: name,
		current: val,
		startup: val,
		origin:  telemetry.OriginDefault,
	}
}

// Get returns the current value.
func (dc *DynamicConfig[T]) Get() T {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.current
}

// update sets a new value if it differs from the current one.
// Returns true if the value was changed.
func (dc *DynamicConfig[T]) update(val T, origin telemetry.Origin) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if reflect.DeepEqual(dc.current, val) {
		return false
	}
	dc.current = val
	dc.origin = origin
	return true
}

// reset restores the startup value. Returns true if the value was changed.
func (dc *DynamicConfig[T]) reset() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if reflect.DeepEqual(dc.current, dc.startup) {
		return false
	}
	dc.current = dc.startup
	dc.origin = telemetry.OriginDefault
	return true
}

// HandleRC processes a remote config update. If val is non-nil, the value is
// updated; if nil, the field is reset to its startup value.
// Returns true if the value was changed.
func (dc *DynamicConfig[T]) HandleRC(val *T) bool {
	if val != nil {
		return dc.update(*val, telemetry.OriginRemoteConfig)
	}
	return dc.reset()
}

// SetOrigin sets the configuration origin.
func (dc *DynamicConfig[T]) SetOrigin(origin telemetry.Origin) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.origin = origin
}

// Origin returns the current configuration origin.
func (dc *DynamicConfig[T]) Origin() telemetry.Origin {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.origin
}

// ToTelemetry returns a telemetry snapshot of the current value and origin.
func (dc *DynamicConfig[T]) ToTelemetry() telemetry.Configuration {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return telemetry.Configuration{
		Name:   dc.cfgName,
		Value:  dc.current,
		Origin: dc.origin,
	}
}
