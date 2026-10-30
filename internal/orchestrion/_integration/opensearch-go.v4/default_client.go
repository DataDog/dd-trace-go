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

// TestCaseDefaultClient calls opensearch.NewDefaultClient, which reads the
// address from OPENSEARCH_URL.
type TestCaseDefaultClient struct {
	base
}

func (tc *TestCaseDefaultClient) Setup(_ context.Context, t *testing.T) {
	t.Setenv("OPENSEARCH_URL", startContainer(t))
	var err error
	tc.client, err = opensearch.NewDefaultClient()
	require.NoError(t, err, "failed to create opensearch client")
}
