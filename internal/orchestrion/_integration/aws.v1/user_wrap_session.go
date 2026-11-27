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

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go/v2/aws"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseUserWrapSession builds a session that the application also wraps
// with the contrib. The call must be traced once, with the options the
// application passed.
type TestCaseUserWrapSession struct {
	counted
}

func (tc *TestCaseUserWrapSession) Setup(_ context.Context, t *testing.T) {
	sess, err := session.NewSession(tc.cfg)
	require.NoError(t, err)
	tc.sess = awstrace.WrapSession(sess, awstrace.WithService(userService))
}

func (*TestCaseUserWrapSession) ExpectedTraces() trace.Traces {
	return expectedRootTraces(userService)
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseUserWrapSession) ExpectedSpanCount() int {
	return 3
}
