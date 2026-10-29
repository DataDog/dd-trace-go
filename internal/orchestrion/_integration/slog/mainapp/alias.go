// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than slog, which the rules must handle the way
// orchestrion does.
import (
	"context"
	stdslog "log/slog"
	"os"
)

func logAliased(ctx context.Context, want []any) {
	stdslog.New(stdslog.NewJSONHandler(os.Stdout, nil)).InfoContext(ctx, "main-alias", want...)
}
