// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
)

// StartMongoDBTestContainer starts a new MongoDB test container and returns the connection string.
func StartMongoDBTestContainer(t testing.TB) (*mongodb.MongoDBContainer, string) {
	ctx := context.Background()

	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(tclog.TestLogger(t)),
		WithTestLogConsumer(t),
		// attempt to reuse this container
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "mongodb",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}),
	}
	container, err := mongodb.Run(ctx,
		"mongo:8",
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	connStr, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	return container, connStr
}
