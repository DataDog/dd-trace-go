// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv1

import (
	"context"
	"testing"

	"example.com/awsv1dep"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a session built in a dependency module, not
// in the module being built, is traced once.
type TestCaseDependency struct {
	counted
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	sess, err := awsv1dep.NewSession(tc.cfg)
	require.NoError(t, err)
	tc.sess = sess
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return expectedRootTraces("aws.dynamodb")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseDependency) ExpectedSpanCount() int {
	return 3
}
