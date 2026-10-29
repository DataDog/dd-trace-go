// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package v1client

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// clientOptions is built during package initialization, which runs before the
// contrib's init unless the instrumented package imports the contrib.
var clientOptions = options.Client()

// Connect connects to uri with clientOptions.
func Connect(ctx context.Context, uri string) (*mongo.Client, error) {
	return mongo.Connect(ctx, clientOptions.ApplyURI(uri))
}
