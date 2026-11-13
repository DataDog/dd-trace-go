// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv1

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgSession is created during package initialization, before the tracer
// starts and before the container's endpoint is known.
var pkgSession = session.Must(session.NewSession(&aws.Config{
	Credentials: credentials.NewStaticCredentials("NOTANACCESSKEY", "NOTASECRETKEY", ""),
	Region:      new("test-region-1337"),
}))

// TestCasePackageVar checks that a session created in a package-level variable
// is traced once.
type TestCasePackageVar struct {
	counted
}

func (*TestCasePackageVar) Setup(context.Context, *testing.T) {}

// Run passes the container's endpoint per client, since pkgSession was created
// before it was known.
func (tc *TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	ddb := dynamodb.New(pkgSession, &aws.Config{Endpoint: tc.cfg.Endpoint})
	_, err := ddb.ListTablesWithContext(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return expectedRootTraces("aws.dynamodb")
}

// ExpectedSpanCount is test.root, the AWS span and its HTTP child.
func (*TestCasePackageVar) ExpectedSpanCount() int {
	return 3
}
