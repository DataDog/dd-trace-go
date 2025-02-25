// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
)

// StartMongoDBContainer starts a new MongoDB test container and returns the connection string.
func StartMongoDBContainer(t testing.TB) (*mongodb.MongoDBContainer, string) {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse mongodb container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "mongodb",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}
	container, err := mongodb.Run(ctx,
		"mongo:6.0.20", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	mongoURI, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	return container, mongoURI
}
