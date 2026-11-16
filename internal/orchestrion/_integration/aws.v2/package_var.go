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

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// These configs are built during package initialization, before the tracer
// starts.
var (
	pkgCreds     = credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", "")
	pkgCfg       = aws.Config{Region: "test-region-1337", Credentials: pkgCreds}
	pkgNewConfig = aws.NewConfig()
)

// TestCasePackageVar checks that a config literal in a package-level variable
// is traced once.
type TestCasePackageVar struct {
	counted
}

func (tc *TestCasePackageVar) Setup(context.Context, *testing.T) {
	tc.cfg = pkgCfg
}

func (tc *TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCasePackageVar) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces("aws.DynamoDB")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCasePackageVar) ExpectedSpanCount() int {
	return 3
}

// TestCasePackageVarNewConfig is TestCasePackageVar for a config built with
// aws.NewConfig.
type TestCasePackageVarNewConfig struct {
	counted
}

func (tc *TestCasePackageVarNewConfig) Setup(context.Context, *testing.T) {
	cfg := *pkgNewConfig
	cfg.Region = "test-region-1337"
	cfg.Credentials = pkgCreds
	tc.cfg = cfg
}

func (tc *TestCasePackageVarNewConfig) Run(ctx context.Context, t *testing.T) {
	tc.runUnderRoot(ctx, t)
}

func (tc *TestCasePackageVarNewConfig) ExpectedTraces() trace.Traces {
	return tc.expectedRootTraces("aws.DynamoDB")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCasePackageVarNewConfig) ExpectedSpanCount() int {
	return 3
}
