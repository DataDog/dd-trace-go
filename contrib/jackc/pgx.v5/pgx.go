// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package pgx

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/contrib/jackc/pgx.v5/v2"

	"github.com/jackc/pgx/v5"
)

type Batch = pgx.Batch

func Connect(ctx context.Context, connString string, opts ...Option) (*pgx.Conn, error) {
	return v2.Connect(ctx, connString, opts...)
}

func ConnectConfig(ctx context.Context, connConfig *pgx.ConnConfig, opts ...Option) (*pgx.Conn, error) {
	return v2.ConnectConfig(ctx, connConfig, opts...)
}
