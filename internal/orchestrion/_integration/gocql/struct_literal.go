// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package gocql

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/require"
)

type TestCaseStructLiteral struct {
	base
}

func (tc *TestCaseStructLiteral) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	var err error
	cluster := gocql.ClusterConfig{
		Hosts:                  []string{tc.hostPort},
		CQLVersion:             "3.0.0",
		Timeout:                11 * time.Second,
		ConnectTimeout:         11 * time.Second,
		NumConns:               2,
		Consistency:            gocql.Quorum,
		MaxPreparedStmts:       1000,
		MaxRoutingKeyInfo:      1000,
		PageSize:               5000,
		DefaultTimestamp:       true,
		MaxWaitSchemaAgreement: 60 * time.Second,
		ReconnectInterval:      60 * time.Second,
		ConvictionPolicy:       &gocql.SimpleConvictionPolicy{},
		ReconnectionPolicy:     &gocql.ConstantReconnectionPolicy{MaxRetries: 3, Interval: 1 * time.Second},
		WriteCoalesceWaitTime:  200 * time.Microsecond,
	}
	tc.session, err = cluster.CreateSession()
	require.NoError(t, err)
	t.Cleanup(func() { tc.session.Close() })
}

func (tc *TestCaseStructLiteral) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (tc *TestCaseStructLiteral) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
