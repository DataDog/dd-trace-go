// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis_test

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	mongotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go.mongodb.org/mongo-driver/mongo"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
)

type Integration struct {
	client   *mongo.Client
	numSpans int
}

func New() *Integration {
	return &Integration{}
}

func (i *Integration) Name() string {
	return "contrib/go.mongodb.org/mongo-driver/mongo"
}

func (i *Integration) Init(t *testing.T) func() {
	t.Helper()
	// connect to MongoDB
	opts := options.Client()
	opts.Monitor = mongotrace.NewMonitor()
	opts.ApplyURI("mongodb://localhost:27017")
	var err error
	i.client, err = mongo.Connect(context.Background(), opts)
	if err != nil {
		panic(err)
	}
	db := i.client.Database("example")
	inventory := db.Collection("inventory")

	inventory.InsertOne(context.Background(), bson.D{
		{Key: "item", Value: "canvas"},
		{Key: "qty", Value: 100},
		{Key: "tags", Value: bson.A{"cotton"}},
		{Key: "size", Value: bson.D{
			{Key: "h", Value: 28},
			{Key: "w", Value: 35.5},
			{Key: "uom", Value: "cm"},
		}},
	})
	i.numSpans++

	return func() {}
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()
	_, err := i.client.
		Database("test-database").
		Collection("test-collection").
		InsertOne(context.Background(), bson.D{{Key: "test-item", Value: "test-value"}})

	assert.Nil(t, err)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}
