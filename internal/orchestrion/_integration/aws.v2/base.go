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

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

type base struct {
	server   testcontainers.Container
	cfg      aws.Config
	endpoint string
}

func (b *base) setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var host, port string
	b.server, host, port = containers.StartDynamoDBTestContainer(t)
	b.endpoint = fmt.Sprintf("http://%s:%s", host, port)
}

func (b *base) run(ctx context.Context, t *testing.T) {
	ddb := dynamodb.NewFromConfig(b.cfg, func(o *dynamodb.Options) { o.BaseEndpoint = &b.endpoint })
	_, err := ddb.ListTables(ctx, nil)
	require.NoError(t, err)
}

func (b *base) expectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "DynamoDB.request",
				"service":  "aws.DynamoDB",
				"resource": "DynamoDB.ListTables",
				"type":     "http",
			},
			Meta: map[string]string{
				"aws.operation": "ListTables",
				"aws.region":    "test-region-1337",
				"aws_service":   "DynamoDB",
				"http.method":   "POST",
				"component":     "aws/aws-sdk-go-v2/aws",
				"span.kind":     "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"service":  "aws.DynamoDB",
						"resource": "POST /",
						"type":     "http",
					},
					Meta: map[string]string{
						"http.method":              "POST",
						"http.status_code":         "200",
						"http.url":                 "/",
						"network.destination.name": "localhost",
						"component":                "net/http",
						"span.kind":                "client",
					},
				},
			},
		},
	}
}
