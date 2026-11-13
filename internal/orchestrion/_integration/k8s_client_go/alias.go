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
	k8srest "k8s.io/client-go/rest"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a config is traced once when the rest
// package is imported under another name.
type TestCaseAliasedImport struct {
	base
}

func (tc *TestCaseAliasedImport) Setup(ctx context.Context, t *testing.T) {
	tc.base.setup(ctx, t)

	client, err := kubernetes.NewForConfig(&k8srest.Config{Host: tc.server.URL})
	require.NoError(t, err)
	tc.base.client = client
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
