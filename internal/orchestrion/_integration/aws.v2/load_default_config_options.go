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
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/require"

	awstrace "github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws"
	"github.com/DataDog/dd-trace-go/contrib/aws/aws-sdk-go-v2/v2/aws/awsconfig"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseLoadDefaultConfigWithTracer loads a config that the application also
// traces with awsconfig.WithDataDogTracer. The call must be traced once, with
// the options the application passed.
type TestCaseLoadDefaultConfigWithTracer struct {
	counted
}

func (tc *TestCaseLoadDefaultConfigWithTracer) Setup(ctx context.Context, t *testing.T) {
	tc.cfg = loadConfig(ctx, t, awsconfig.WithDataDogTracer(awstrace.WithService(userService)))
}

func (tc *TestCaseLoadDefaultConfigWithTracer) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCaseLoadDefaultConfigWithTracer) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces(userService)
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseLoadDefaultConfigWithTracer) ExpectedSpanCount() int {
	return 3
}

// TestCaseLoadDefaultConfigWithAPIOptions loads a config with API options of
// its own. The config package replaces the config's API options with them, and
// the call must still be traced once.
type TestCaseLoadDefaultConfigWithAPIOptions struct {
	counted
}

func (tc *TestCaseLoadDefaultConfigWithAPIOptions) Setup(ctx context.Context, t *testing.T) {
	tc.cfg = loadConfig(ctx, t, withUserAgentAPIOption())
}

func (tc *TestCaseLoadDefaultConfigWithAPIOptions) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCaseLoadDefaultConfigWithAPIOptions) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces("aws.DynamoDB")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseLoadDefaultConfigWithAPIOptions) ExpectedSpanCount() int {
	return 3
}

// TestCaseLoadDefaultConfigWithAPIOptionsAndTracer combines API options of
// its own with awsconfig.WithDataDogTracer. The call must be traced once, with
// the options the application passed.
type TestCaseLoadDefaultConfigWithAPIOptionsAndTracer struct {
	counted
}

func (tc *TestCaseLoadDefaultConfigWithAPIOptionsAndTracer) Setup(ctx context.Context, t *testing.T) {
	tc.cfg = loadConfig(ctx, t,
		withUserAgentAPIOption(),
		awsconfig.WithDataDogTracer(awstrace.WithService(userService)),
	)
}

func (tc *TestCaseLoadDefaultConfigWithAPIOptionsAndTracer) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCaseLoadDefaultConfigWithAPIOptionsAndTracer) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces(userService)
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseLoadDefaultConfigWithAPIOptionsAndTracer) ExpectedSpanCount() int {
	return 3
}

func loadConfig(ctx context.Context, t *testing.T, opts ...func(*config.LoadOptions) error) aws.Config {
	opts = append([]func(*config.LoadOptions) error{
		config.WithRegion("test-region-1337"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", "")),
	}, opts...)
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	require.NoError(t, err)
	return cfg
}

// withUserAgentAPIOption sets API options the way an application adds its own
// middleware.
func withUserAgentAPIOption() config.LoadOptionsFunc {
	return config.WithAPIOptions([]func(*middleware.Stack) error{
		awsmiddleware.AddUserAgentKeyValue("app", "awsv2-test"),
	})
}
