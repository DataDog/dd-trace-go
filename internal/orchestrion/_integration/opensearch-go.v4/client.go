// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package opensearch

import (
	"context"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/stretchr/testify/require"
)

// TestCaseClient calls opensearch.NewClient directly.
type TestCaseClient struct {
	base
}

func (tc *TestCaseClient) Setup(_ context.Context, t *testing.T) {
	var err error
	tc.client, err = opensearch.NewClient(opensearch.Config{
		Addresses: []string{startContainer(t)},
	})
	require.NoError(t, err, "failed to create opensearch client")
}
