// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"context"
	"testing"

	mongotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go.mongodb.org/mongo-driver/mongo"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type GoMongo struct {
	client   *mongo.Client
	numSpans int
	opts     []mongotrace.Option
}

func NewGoMongo() *GoMongo {
	return &GoMongo{
		opts: make([]mongotrace.Option, 0),
	}
}

func (i *GoMongo) WithServiceName(name string) {
	i.opts = append(i.opts, mongotrace.WithServiceName(name))
}

func (i *GoMongo) Name() string {
	return "go.mongodb.org/mongo-driver/mongo"
}

func (i *GoMongo) Init(t *testing.T) {
	t.Helper()
	// connect to MongoDB
	opts := options.Client()
	opts.Monitor = mongotrace.NewMonitor(i.opts...)
	opts.ApplyURI("mongodb://localhost:27017/?connect=direct")
	var err error
	i.client, err = mongo.Connect(context.Background(), opts)
	require.NoError(t, err)

	// _, err = i.client.
	// 	Database("test-database").
	// 	Collection("test-collection").
	// 	InsertOne(context.Background(), bson.D{{Key: "test-item", Value: "test-value"}})
	// require.NoError(t, err)
	// i.numSpans++

	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *GoMongo) GenSpans(t *testing.T) {
	t.Helper()
	_, err := i.client.
		Database("test-database").
		Collection("test-collection").
		InsertOne(context.Background(), bson.D{{Key: "test-item", Value: "test-value"}})

	require.NoError(t, err)
	i.numSpans++
}

func (i *GoMongo) NumSpans() int {
	return i.numSpans
}
