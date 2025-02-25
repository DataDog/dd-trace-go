// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package awsv2

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

type TestCaseStructLiteral struct {
	base
}

func (tc *TestCaseStructLiteral) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	tc.cfg = aws.Config{
		Region:       "test-region-1337",
		Credentials:  credentials.NewStaticCredentialsProvider("NOTANACCESSKEY", "NOTASECRETKEY", ""),
		BaseEndpoint: aws.String(fmt.Sprintf("http://%s:%s", tc.host, tc.port)),
	}
}

func (tc *TestCaseStructLiteral) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (tc *TestCaseStructLiteral) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
