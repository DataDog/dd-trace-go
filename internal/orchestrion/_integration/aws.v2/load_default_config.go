// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package awsv2

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/require"
)

type TestCaseLoadDefaultConfig struct {
	base
}

func (tc *TestCaseLoadDefaultConfig) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("test-region-1337"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", "")),
	)
	require.NoError(t, err)
	tc.cfg = cfg
}

func (tc *TestCaseLoadDefaultConfig) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (tc *TestCaseLoadDefaultConfig) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
