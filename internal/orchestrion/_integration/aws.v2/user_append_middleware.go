// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv2

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseUserAppendMiddleware builds a config literal that the application
// also traces with the contrib. The call must be traced once, with the options
// the application passed.
type TestCaseUserAppendMiddleware struct {
	counted
}

func (tc *TestCaseUserAppendMiddleware) Setup(context.Context, *testing.T) {
	cfg := aws.Config{
		Region:      "test-region-1337",
		Credentials: credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", ""),
	}
	awstrace.AppendMiddleware(&cfg, awstrace.WithService(userService))
	tc.cfg = cfg
}

func (tc *TestCaseUserAppendMiddleware) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCaseUserAppendMiddleware) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces(userService)
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseUserAppendMiddleware) ExpectedSpanCount() int {
	return 3
}
