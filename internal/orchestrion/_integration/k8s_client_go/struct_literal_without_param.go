// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package k8sclientgo

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type TestCaseStructLiteralWithoutParam struct {
	base
	wtCalled bool
}

func (tc *TestCaseStructLiteralWithoutParam) Setup(ctx context.Context, t *testing.T) {
	tc.base.setup(ctx, t)

	cfg := &rest.Config{
		Host: tc.server.URL,
	}

	client, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	tc.base.client = client
}

func (tc *TestCaseStructLiteralWithoutParam) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (tc *TestCaseStructLiteralWithoutParam) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
