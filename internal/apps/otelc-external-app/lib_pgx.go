// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func init() {
	registerLibrary("pgx", func() {
		// The pool opens no connection until first used.
		pool, err := pgxpool.New(context.Background(), "postgres://localhost/db")
		if err != nil {
			panic(err)
		}
		pool.Close()
	})
}
