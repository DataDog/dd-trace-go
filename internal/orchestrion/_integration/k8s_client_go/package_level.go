// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package k8sclientgo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// packageLevelConfig is a literal outside any function.
var packageLevelConfig = rest.Config{}

// TestCasePackageLevelConfig checks that a config written as a package-level
// literal is traced once.
type TestCasePackageLevelConfig struct {
	base
}

func (tc *TestCasePackageLevelConfig) Setup(ctx context.Context, t *testing.T) {
	tc.base.setup(ctx, t)

	cfg := packageLevelConfig
	cfg.Host = tc.server.URL
	client, err := kubernetes.NewForConfig(&cfg)
	require.NoError(t, err)
	tc.base.client = client
}

func (tc *TestCasePackageLevelConfig) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCasePackageLevelConfig) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCasePackageLevelConfig) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
