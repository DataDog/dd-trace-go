// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package zapdep logs with zap inside a dependency module.
package zapdep

import (
	"context"

	"go.uber.org/zap"
)

// Log logs msg with logger and with its sugared logger, with ctx in scope.
func Log(ctx context.Context, logger *zap.Logger, msg string) {
	_ = ctx
	logger.Info(msg)
	logger.Sugar().Infow(msg, "sugared", true)
}
