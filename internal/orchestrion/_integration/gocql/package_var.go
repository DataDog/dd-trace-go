// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"

	"github.com/gocql/gocql"
	"github.com/stretchr/testify/require"

	gocqltrace "github.com/DataDog/dd-trace-go/contrib/gocql/gocql/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgCluster is built during package initialization, before the tracer starts
// and before the container's address is known.
var pkgCluster = gocql.NewCluster()

// TestCasePackageVar checks that a cluster built in a package-level variable is
// traced.
type TestCasePackageVar struct {
	base
}

func (tc *TestCasePackageVar) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	// Checked here because the CreateSession hook would trace the session even
	// if the NewCluster call were not rewritten.
	require.IsType(t, &gocqltrace.Observer{}, pkgCluster.QueryObserver)
	pkgCluster.Hosts = []string{tc.hostPort}
	var err error
	tc.session, err = pkgCluster.CreateSession()
	require.NoError(t, err)
	t.Cleanup(func() { tc.session.Close() })
}

func (tc *TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (tc *TestCasePackageVar) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
