// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type TestCase struct {
	client *mongo.Client
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, mongoURI := containers.StartMongoDBTestContainer(t)

	opts := options.Client()
	opts.ApplyURI(mongoURI)
	client, err := mongo.Connect(opts)
	require.NoError(t, err)
	tc.client = client

	t.Cleanup(func() {
		// Using a new 10s-timeout context, as we may be running cleanup after the original context expired.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, tc.client.Disconnect(ctx))
	})
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	db := tc.client.Database("test")
	c := db.Collection("coll")

	_, err := c.InsertOne(ctx, bson.M{"test_key": "test_value"})
	require.NoError(t, err)
	r := c.FindOne(ctx, bson.M{"test_key": "test_value"})
	require.NoError(t, r.Err())
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "mongodb.query",
						"service":  "mongo",
						"resource": "mongo.insert",
						"type":     "mongodb",
					},
					Meta: map[string]string{
						"component": "go.mongodb.org/mongo-driver.v2",
						"span.kind": "client",
						"db.system": "mongodb",
					},
				},
				{
					Tags: map[string]any{
						"name":     "mongodb.query",
						"service":  "mongo",
						"resource": "mongo.find",
						"type":     "mongodb",
					},
					Meta: map[string]string{
						"component": "go.mongodb.org/mongo-driver.v2",
						"span.kind": "client",
						"db.system": "mongodb",
					},
				},
			},
		},
	}
}
