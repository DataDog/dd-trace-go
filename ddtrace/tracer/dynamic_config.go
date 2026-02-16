// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// dynamicConfig is a thread-safe generic data structure to represent configuration fields.
// It's designed to satisfy the dynamic configuration semantics (i.e reset, update, apply configuration changes).
// This structure will be extended to track the origin of configuration values as well (e.g remote_config, env_var).
type dynamicConfig[T any] struct {
	mu locking.RWMutex

	// holds the current configuration value
	current T // +checklocks:mu
	// holds the startup configuration value
	startup T // +checklocks:mu
	// holds the name of the configuration, has to be compatible with telemetry.Configuration.Name
	cfgName string // +checklocks:mu
	// holds the origin of the current configuration value (currently only supports remote_config, empty otherwise)
	cfgOrigin telemetry.Origin // +checklocks:mu
	// executes any config-specific operations to propagate the update properly, returns whether the update was applied
	apply func(T) bool // +checklocks:mu
	// compares two configuration values, this is used to avoid unnecessary config and telemetry updates
	equal func(x, y T) bool // +checklocks:mu
}

func newDynamicConfig[T any](name string, val T, apply func(T) bool, equal func(x, y T) bool) dynamicConfig[T] {
	return dynamicConfig[T]{
		cfgName:   name,
		current:   val,
		startup:   val,
		cfgOrigin: telemetry.OriginDefault,
		apply:     apply,
		equal:     equal,
	}
}

// get returns the current configuration value
func (dc *dynamicConfig[T]) get() T {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.current
}

func (dc *dynamicConfig[T]) set(f func(current T) T) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.current = f(dc.current)
}

// update applies a new configuration value
func (dc *dynamicConfig[T]) update(val T, origin telemetry.Origin) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.equal(dc.current, val) {
		return false
	}
	dc.current = val
	dc.cfgOrigin = origin
	return dc.apply(val)
}

// reset re-applies the startup configuration value
func (dc *dynamicConfig[T]) reset() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.equal(dc.current, dc.startup) {
		return false
	}
	dc.current = dc.startup
	// TODO: set the origin to the startup value's origin
	dc.cfgOrigin = telemetry.OriginDefault
	return dc.apply(dc.startup)
}

// handleRC processes a new configuration value from remote config
// Returns whether the configuration value has been updated or not
func (dc *dynamicConfig[T]) handleRC(val *T) bool {
	if val != nil {
		return dc.update(*val, telemetry.OriginRemoteConfig)
	}
	return dc.reset()
}

// toTelemetry returns the current configuration value as telemetry.Configuration
func (dc *dynamicConfig[T]) toTelemetry() telemetry.Configuration {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return telemetry.Configuration{
		Name:   dc.cfgName,
		Value:  dc.current,
		Origin: dc.cfgOrigin,
	}
}

// setOrigin safely sets the configuration origin
func (dc *dynamicConfig[T]) setOrigin(origin telemetry.Origin) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.cfgOrigin = origin
}

// Origin safely reads the configuration origin
func (dc *dynamicConfig[T]) Origin() telemetry.Origin {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.cfgOrigin
}

// getCurrentAndOrigin atomically reads both current value and origin
// This prevents TOCTOU bugs when both values are needed
func (dc *dynamicConfig[T]) getCurrentAndOrigin() (T, telemetry.Origin) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.current, dc.cfgOrigin
}

// setStartup updates the startup value (used during initialization)
func (dc *dynamicConfig[T]) setStartup(val T) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.startup = val
}

func equal[T comparable](x, y T) bool {
	return x == y
}

// equalSlice compares two slices of comparable values
// The comparison takes into account the order of the elements
func equalSlice[T comparable](x, y []T) bool {
	if len(x) != len(y) {
		return false
	}
	for i, v := range x {
		if v != y[i] {
			return false
		}
	}
	return true
}

// equalMap compares two maps of comparable keys and values
func equalMap[T comparable](x, y map[T]interface{}) bool {
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
