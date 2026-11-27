// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"

	"go.uber.org/zap"
)

func init() {
	registerLibrary("zap", func() {
		logWithContext(context.Background())
	})
}

// logWithContext logs with a context.Context in scope, which the zap rules
// need to rewrite the call.
func logWithContext(ctx context.Context) {
	_ = ctx
	zap.NewNop().Info("otelc external app")
}
