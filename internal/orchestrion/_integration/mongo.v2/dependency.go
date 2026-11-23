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

	"example.com/mongodep/v2client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency connects with options a dependency module builds in a
// package-level variable.
type TestCaseDependency struct {
	client *mongo.Client
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, uri := containers.StartMongoDBTestContainer(t)

	client, err := v2client.Connect(uri)
	require.NoError(t, err)
	tc.client = client
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.client.Disconnect(ctx))
	})
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	_, err := tc.client.Database("test").Collection("dep").InsertOne(ctx, bson.M{"test_key": "test_value"})
	require.NoError(t, err)
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
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
						"component": "go.mongodb.org/mongo-driver.v2",
					},
				},
			},
		},
	}
}
