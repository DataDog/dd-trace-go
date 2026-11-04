// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package awsv1

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

const userService = "aws-user-service"

// counted starts the container before the harness starts the tracer, so the
// Docker API calls it makes are not counted and a test case can pin the exact
// span count.
type counted struct {
	cfg  *aws.Config
	sess *session.Session
}

func (c *counted) PreBootstrap(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	_, host, port := containers.StartDynamoDBTestContainer(t)
	c.cfg = &aws.Config{
		Credentials: credentials.NewStaticCredentials("NOTANACCESSKEY", "NOTASECRETKEY", ""),
		Endpoint:    new(fmt.Sprintf("http://%s:%s", host, port)),
		Region:      new("test-region-1337"),
	}
}

// Run calls ListTables under a test.root span, so the expected traces can
// require the AWS span to be its direct child. A second AWS span would sit
// between the two.
func (c *counted) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	_, err := dynamodb.New(c.sess).ListTablesWithContext(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
}

// expectedRootTraces expects one AWS span under test.root, reported with
// service.
func expectedRootTraces(service string) trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "dynamodb.command",
						"service":  service,
						"resource": "dynamodb.ListTables",
						"type":     "http",
					},
					Meta: map[string]string{
						"aws.operation": "ListTables",
						"component":     "aws/aws-sdk-go/aws",
						"span.kind":     "client",
					},
				},
			},
		},
	}
}
