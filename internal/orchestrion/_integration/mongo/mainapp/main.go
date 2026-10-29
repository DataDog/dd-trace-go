// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	uri := os.Getenv("MONGODB_URI") //nolint:forbidigo // set by the test, not a tracer setting
	insert(ctx, options.Client().ApplyURI(uri), "main")
	insertAliased(ctx, uri)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func insert(ctx context.Context, opts *options.ClientOptions, db string) {
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := client.Disconnect(ctx); err != nil {
			panic(err)
		}
	}()
	if _, err := client.Database(db).Collection("mainapp").InsertOne(ctx, bson.M{"db": db}); err != nil {
		panic(err)
	}
}
