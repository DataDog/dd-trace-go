// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package v2client

import (
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// clientOptions is built during package initialization, which runs before the
// contrib's init unless the instrumented package imports the contrib.
var clientOptions = options.Client()

// Connect connects to uri with clientOptions.
func Connect(uri string) (*mongo.Client, error) {
	return mongo.Connect(clientOptions.ApplyURI(uri))
}
