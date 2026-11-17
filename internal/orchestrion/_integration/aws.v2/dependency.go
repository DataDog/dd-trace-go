// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv2

import (
	"context"
	"testing"

	"example.com/awsv2dep"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that configs built in a dependency module, not in
// the module being built, are traced once each. Each call runs under its own
// test.root span, which must be the direct parent of one AWS span.
type TestCaseDependency struct {
	counted
	cfgs map[string]aws.Config
}

func (tc *TestCaseDependency) Setup(context.Context, *testing.T) {
	tc.cfgs = awsv2dep.Configs("test-region-1337",
		credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", ""))
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	for style, cfg := range tc.cfgs {
		span, ctx := tracer.StartSpanFromContext(ctx, "test.root", tracer.ResourceName(style))
		ddb := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = &tc.endpoint })
		_, err := ddb.ListTables(ctx, nil)
		span.Finish()
		require.NoError(t, err, style)
	}
}

func (tc *TestCaseDependency) ExpectedTraces() trace.Traces {
	traces := make(trace.Traces, 0, len(tc.cfgs))
	for style := range tc.cfgs {
		traces = append(traces, &trace.Trace{
			Tags:     map[string]any{"name": "test.root", "resource": style},
			Children: tc.base.expectedTraces(),
		})
	}
	return traces
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child per style.
func (tc *TestCaseDependency) ExpectedSpanCount() int {
	return 3 * len(tc.cfgs)
}
