// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bootstrap

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	appSecStackTraceEnabledKey = "DD_APPSEC_STACK_TRACE_ENABLED"
	appSecStackTraceDepthKey   = "DD_APPSEC_MAX_STACK_TRACE_DEPTH"
	appSecStackTraceMaxDepth   = 32
)

// AppSecStackTraceSnapshot contains the process-wide AppSec stack-trace
// settings and the raw metadata needed for later configuration telemetry.
type AppSecStackTraceSnapshot struct {
	Enabled       bool
	MaxDepth      int
	TopFrameDepth int

	EnabledRaw     string
	EnabledPresent bool
	EnabledError   error
	DepthRaw       string
	DepthPresent   bool
	DepthError     error
}

var (
	appSecStackTraceSnapshot AppSecStackTraceSnapshot
	appSecStackTraceOnce     sync.Once
	appSecStackTraceReported atomic.Bool
)

// AppSecStackTrace returns the process-wide package-init stack-trace settings.
// The environment and legacy parse diagnostics are evaluated at most once.
func AppSecStackTrace() AppSecStackTraceSnapshot {
	appSecStackTraceOnce.Do(func() {
		appSecStackTraceSnapshot = resolveAppSecStackTrace()
	})
	return appSecStackTraceSnapshot
}

// ClaimAppSecStackTraceTelemetry returns the cached settings and reports
// whether the caller owns their single telemetry projection.
func ClaimAppSecStackTraceTelemetry() (AppSecStackTraceSnapshot, bool) {
	snapshot := AppSecStackTrace()
	return snapshot, appSecStackTraceReported.CompareAndSwap(false, true)
}

func resolveAppSecStackTrace() AppSecStackTraceSnapshot {
	snapshot := AppSecStackTraceSnapshot{
		Enabled:       true,
		MaxDepth:      appSecStackTraceMaxDepth,
		TopFrameDepth: appSecStackTraceMaxDepth / 4,
	}

	snapshot.EnabledRaw = env.Get(appSecStackTraceEnabledKey)
	snapshot.EnabledPresent = snapshot.EnabledRaw != ""
	if snapshot.EnabledPresent {
		enabled, err := strconv.ParseBool(snapshot.EnabledRaw)
		snapshot.EnabledError = err
		if err != nil {
			log.Error(
				"Failed to parse DD_APPSEC_STACK_TRACE_ENABLED env var as boolean: (using default value: %t) %v",
				true,
				err.Error(),
			)
		} else {
			snapshot.Enabled = enabled
		}
	}

	snapshot.DepthRaw = env.Get(appSecStackTraceDepthKey)
	snapshot.DepthPresent = snapshot.DepthRaw != ""
	var depth int
	if snapshot.DepthPresent {
		var err error
		depth, err = strconv.Atoi(snapshot.DepthRaw)
		snapshot.DepthError = err
	}

	if !snapshot.Enabled {
		if snapshot.DepthPresent {
			log.Warn("Ignoring DD_APPSEC_MAX_STACK_TRACE_DEPTH because stacktrace generation is disable")
		}
		return snapshot
	}
	if snapshot.DepthError != nil {
		err := snapshot.DepthError
		if depth <= 0 {
			err = errors.New("value is not a strictly positive integer")
		}
		log.Error(
			"Failed to parse DD_APPSEC_MAX_STACK_TRACE_DEPTH env var as a positive integer: (using default value: %d) %v",
			appSecStackTraceMaxDepth,
			err.Error(),
		)
		return snapshot
	}
	if snapshot.DepthPresent {
		snapshot.MaxDepth = depth
		snapshot.TopFrameDepth = depth / 4
	}
	return snapshot
}

// ResetAppSecStackTraceForTesting clears the cached AppSec stack-trace
// bootstrap settings. It is not safe to call concurrently with AppSecStackTrace.
func ResetAppSecStackTraceForTesting() {
	appSecStackTraceSnapshot = AppSecStackTraceSnapshot{}
	appSecStackTraceOnce = sync.Once{}
	appSecStackTraceReported.Store(false)
}
