// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

// attachProvider wires the given provider to the global RC subscription
// managed by internal/openfeature. If FFE_FLAGS is subscribed, it replays the
// latest config, keeps the detach function for Shutdown, and returns true.
// Otherwise it returns false.
func attachProvider(p *DatadogProvider) bool {
	detach, ok := internalffe.AttachCallback(p.rcCallback)
	if !ok {
		return false
	}
	p.mu.Lock()
	p.rcDetach = detach
	p.mu.Unlock()
	return true
}
