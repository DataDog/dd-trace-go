// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport connects with client options built from the options
// package imported under another name.
type TestCaseAliasedImport struct {
	uri    string
	client *mongo.Client
}

// PreBootstrap starts the container before the tracer, so the Docker API
// calls it makes are not counted.
func (tc *TestCaseAliasedImport) PreBootstrap(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, tc.uri = containers.StartMongoDBTestContainer(t)
}

func (tc *TestCaseAliasedImport) Setup(ctx context.Context, t *testing.T) {
	client, err := mongo.Connect(ctx, mongooptions.Client().ApplyURI(tc.uri))
	require.NoError(t, err)
	tc.client = client
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.client.Disconnect(ctx))
	})
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	_, err := tc.client.Database("test").Collection("alias").InsertOne(ctx, bson.M{"test_key": "test_value"})
	require.NoError(t, err)
}

// ExpectedSpanCount is test.root and the insert.
func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "mongodb.query",
						"resource": "mongo.insert",
					},
					Meta: map[string]string{
						"component": "go.mongodb.org/mongo-driver/mongo",
					},
				},
			},
		},
	}
}
