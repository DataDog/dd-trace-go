// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package pgxconn opens pgx connections and pools inside a dependency module.
package pgxconn

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a connection with pgx.Connect.
func Connect(ctx context.Context, dbURL string) (*pgx.Conn, error) {
	return pgx.Connect(ctx, dbURL)
}

// NewPool creates a pool with pgxpool.New.
func NewPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dbURL)
}
