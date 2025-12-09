// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/require"
)

type TestCaseNewCluster struct {
	base
}

func (tc *TestCaseNewCluster) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	var err error
	cluster := gocql.NewCluster(tc.hostPort)
	tc.session, err = cluster.CreateSession()
	require.NoError(t, err)
	t.Cleanup(func() { tc.session.Close() })
}

func (tc *TestCaseNewCluster) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (tc *TestCaseNewCluster) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
