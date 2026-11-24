// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv1

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseNewSession pins the span count for a session the application does
// not wrap itself.
type TestCaseNewSession struct {
	counted
}

func (tc *TestCaseNewSession) Setup(_ context.Context, t *testing.T) {
	sess, err := session.NewSession(tc.cfg)
	require.NoError(t, err)
	tc.sess = sess
}

func (*TestCaseNewSession) ExpectedTraces() trace.Traces {
	return expectedRootTraces("aws.dynamodb")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseNewSession) ExpectedSpanCount() int {
	return 3
}
