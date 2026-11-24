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

// TestCaseNewSessionWithOptions pins the span count for a session built with
// session.NewSessionWithOptions.
type TestCaseNewSessionWithOptions struct {
	counted
}

func (tc *TestCaseNewSessionWithOptions) Setup(_ context.Context, t *testing.T) {
	sess, err := session.NewSessionWithOptions(session.Options{Config: *tc.cfg})
	require.NoError(t, err)
	tc.sess = sess
}

func (*TestCaseNewSessionWithOptions) ExpectedTraces() trace.Traces {
	return expectedRootTraces("aws.dynamodb")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseNewSessionWithOptions) ExpectedSpanCount() int {
	return 3
}

// TestCaseSessionNew pins the span count for a session built with the
// deprecated session.New.
type TestCaseSessionNew struct {
	counted
}

func (tc *TestCaseSessionNew) Setup(context.Context, *testing.T) {
	tc.sess = session.New(tc.cfg) //nolint:staticcheck // session.New is deprecated but still supported
}

func (*TestCaseSessionNew) ExpectedTraces() trace.Traces {
	return expectedRootTraces("aws.dynamodb")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseSessionNew) ExpectedSpanCount() int {
	return 3
}
