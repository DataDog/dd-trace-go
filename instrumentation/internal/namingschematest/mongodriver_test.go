// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	mongotrace "github.com/DataDog/dd-trace-go/contrib/go.mongodb.org/mongo-driver/v2/mongo"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var mongoDriverTest = harness.TestCase{
	Name: instrumentation.PackageMongoDriver,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []mongotrace.Option
		if serviceOverride != "" {
			opts = append(opts, mongotrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		addr := fmt.Sprintf("mongodb://localhost:27017/?connect=direct")
		mongopts := options.Client()
		mongopts.Monitor = mongotrace.NewMonitor(opts...)
		mongopts.ApplyURI(addr)
		client, err := mongo.Connect(context.Background(), mongopts)
		require.NoError(t, err)
		_, err = client.
			Database("test-database").
			Collection("test-collection").
			InsertOne(context.Background(), bson.D{{Key: "test-item", Value: "test-value"}})
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"mongo"},
		DDService:       []string{"mongo"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "mongodb.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "mongodb.query", spans[0].OperationName())
	},
}
