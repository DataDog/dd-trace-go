// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelapiprovider

import (
	"context"
	"testing"
)

// bridgeEnabled turns on the OpenTelemetry API bridge. The bridge does nothing unless
// DD_TRACE_OTEL_ENABLED is true.
type bridgeEnabled struct{}

func (bridgeEnabled) PreBootstrap(_ context.Context, t *testing.T) {
	t.Setenv("DD_TRACE_OTEL_ENABLED", "true")
}
