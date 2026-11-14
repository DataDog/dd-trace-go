// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv2

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a config literal is traced once when the
// aws package is imported under another name.
type TestCaseAliasedImport struct {
	counted
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	tc.cfg = awssdk.Config{
		Region:      "test-region-1337",
		Credentials: credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", ""),
	}
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces("aws.DynamoDB")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 3
}

// TestCaseAliasedNewConfig is TestCaseAliasedImport for a config built with
// aws.NewConfig.
type TestCaseAliasedNewConfig struct {
	counted
}

func (tc *TestCaseAliasedNewConfig) Setup(context.Context, *testing.T) {
	cfg := awssdk.NewConfig()
	cfg.Region = "test-region-1337"
	cfg.Credentials = credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", "")
	tc.cfg = *cfg
}

func (tc *TestCaseAliasedNewConfig) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCaseAliasedNewConfig) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces("aws.DynamoDB")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCaseAliasedNewConfig) ExpectedSpanCount() int {
	return 3
}
