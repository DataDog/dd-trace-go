// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package awsv1

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

type TestCase struct {
	server testcontainers.Container
	cfg    *aws.Config
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	server, host, port := containers.StartDynamoDBTestContainer(t)
	tc.server = server

	tc.cfg = &aws.Config{
		Credentials: credentials.NewStaticCredentials("NOTANACCESSKEY", "NOTASECRETKEY", ""),
		Endpoint:    aws.String(fmt.Sprintf("http://%s:%s", host, port)),
		Region:      aws.String("test-region-1337"),
	}
}

func (tc *TestCase) Run(_ context.Context, t *testing.T) {
	ddb := dynamodb.New(session.Must(session.NewSession(tc.cfg)))
	_, err := ddb.ListTables(nil)
	require.NoError(t, err)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "dynamodb.command",
				"service":  "aws.dynamodb",
				"resource": "dynamodb.ListTables",
				"type":     "http",
			},
			Meta: map[string]string{
				"aws.operation":    "ListTables",
				"aws.region":       "test-region-1337",
				"aws_service":      "dynamodb",
				"http.method":      "POST",
				"http.status_code": "200",
				"component":        "aws/aws-sdk-go/aws",
				"span.kind":        "client",
			},
		},
	}
}
