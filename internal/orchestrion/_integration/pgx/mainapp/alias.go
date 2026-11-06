// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

// Imported under another name than pgx, which the rules must handle the way
// orchestrion does.
import (
	"context"

	pgxv5 "github.com/jackc/pgx/v5"
)

func connectAliased(ctx context.Context) {
	if _, err := pgxv5.Connect(ctx, dsn); err == nil {
		panic("pgxv5.Connect: expected an error")
	}
}
