// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"net/url"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartRedisTestContainer starts a new Redis test container and returns the connection string.
func StartRedisTestContainer(t testing.TB) (*redis.RedisContainer, string) {
	ctx := context.Background()
	exposedPort := "6379/tcp"
	waitReadyCmd := []string{
		"redis-cli",
		"ping",
	}

	container, err := redis.Run(ctx,
		"redis:7-alpine", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("* Ready to accept connections"),
				wait.ForExposedPort(),
				wait.ForListeningPort(nat.Port(exposedPort)),
				wait.ForExec(waitReadyCmd),
			),
		),
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	connStr, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	redisURL, err := url.Parse(connStr)
	require.NoError(t, err)

	return container, redisURL.Host
}
