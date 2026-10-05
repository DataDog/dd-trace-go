// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

// attachProvider wires the given provider to the global RC subscription
// managed by internal/openfeature. If the tracer already subscribed to
// FFE_FLAGS, it replays any buffered config and returns true. Otherwise
// returns false, meaning the caller should fall back to its own RC subscription.
func attachProvider(p *DatadogProvider) bool {
	return internalffe.AttachCallback(p.rcCallback)
}
