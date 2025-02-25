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
	testpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartPostgresContainer starts a new Postgres test container and returns the connection string.
func StartPostgresContainer(t testing.TB) (*testpostgres.PostgresContainer, string) {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
		// https://golang.testcontainers.org/modules/postgres/#wait-strategies_1
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse postgres container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "postgres",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := testpostgres.Run(ctx,
		"docker.io/postgres:16-alpine", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	dbURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return container, dbURL
}
