// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Snapshots bundle the config fields read by a single hot-path caller so they
// can be fetched under one RLock instead of one RLock per field. The win is
// not from blocking — these are all readers — but from cache-line contention
// on the RWMutex's reader counter when many goroutines call the hot path
// concurrently.
//
// Add a new struct + method below when a new hot path needs more than ~3 fields
// under the lock.

package config

type SpanStartSnapshot struct {
	ServiceName             string
	Env                     string
	Version                 string
	Hostname                string
	ReportHostname          bool
	DebugStack              bool
	DebugAbandonedSpans     bool
	ProfilerHotspotsEnabled bool
	ProfilerEndpoints       bool
}

// SpanStartSnapshot returns a snapshot of the config fields read by
// tracer.StartSpan. Service mappings are not included because the lookup key
// (the resolved span service) isn't known until after this snapshot is read.
func (c *Config) SpanStartSnapshot() SpanStartSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return SpanStartSnapshot{
		ServiceName:             c.serviceName,
		Env:                     c.env,
		Version:                 c.version,
		Hostname:                c.hostname,
		ReportHostname:          c.reportHostname,
		DebugStack:              c.debugStack,
		DebugAbandonedSpans:     c.debugAbandonedSpans,
		ProfilerHotspotsEnabled: c.profilerHotspots,
		ProfilerEndpoints:       c.profilerEndpoints,
	}
}
