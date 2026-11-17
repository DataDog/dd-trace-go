// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package pgx

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgPool is created during package initialization, before the tracer starts
// and before the database exists. It opens no connection until first used.
var pkgPool, pkgPoolErr = pgxpool.NewWithConfig(context.Background(), pkgPoolConfig())

// pkgPoolURL is the database pkgPool connects to.
var pkgPoolURL string

// TestCasePackageVar checks that a pool created in a package-level variable is
// traced once.
type TestCasePackageVar struct{}

// PreBootstrap starts the database before the tracer, so the container's calls
// to Docker stay out of the span count.
func (*TestCasePackageVar) PreBootstrap(ctx context.Context, t *testing.T) {
	pkgPoolURL = startPostgres(ctx, t)
}

func (*TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	require.NoError(t, pkgPoolErr)
}

func (*TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	selectInt(ctx, t, pkgPool, 11)
	pkgPool.Close()
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				acquireSpan(),
				querySpan(selectSQL(11)),
			},
		},
	}
}

// ExpectedSpanCount is the root, and an acquire, a connect, a query and a
// prepare span.
func (*TestCasePackageVar) ExpectedSpanCount() int {
	return 5
}

// pkgPoolConfig points each new connection at pkgPoolURL, which is only known
// once the test starts the database.
func pkgPoolConfig() *pgxpool.Config {
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/db?sslmode=disable")
	if err != nil {
		panic(err)
	}
	cfg.BeforeConnect = func(_ context.Context, conn *pgx.ConnConfig) error {
		db, err := pgx.ParseConfig(pkgPoolURL)
		if err != nil {
			return err
		}
		// Copy only the address and credentials, to keep the tracer the
		// instrumentation set on conn.
		conn.Host, conn.Port, conn.Database = db.Host, db.Port, db.Database
		conn.User, conn.Password = db.User, db.Password
		return nil
	}
	return cfg
}
