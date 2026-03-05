// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package internal

import "sync/atomic"

var (
	tracerInit atomic.Bool
)

func SetTracerInitialized(val bool) {
	tracerInit.Store(val)
}

func TracerInitialized() bool {
	return tracerInit.Load()
}

// KeyServiceSource is the internal span meta key for tracking the origin of a
// service name override. It is intercepted by the tracer and stored in an
// internal field rather than written directly to span meta.
const KeyServiceSource = "_dd.srv_src"

// ServiceOverride bundles a service name with its source for atomic tag
// handling, avoiding map iteration order issues when both need to be set
// together during span initialization.
type ServiceOverride struct {
	Name   string
	Source string
}
