// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartDynamoDBTestContainer starts a new dynamoDB test container and returns the necessary information to connect
// to it.
func StartDynamoDBTestContainer(t testing.TB) (testcontainers.Container, string, string) {
	exposedPort := "8000/tcp"
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "amazon/dynamodb-local:latest", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
			ExposedPorts: []string{exposedPort},
			WaitingFor:   wait.ForHTTP("").WithStatusCodeMatcher(func(int) bool { return true }),
			WorkingDir:   "/home/dynamodblocal",
			Cmd: []string{
				"-jar", "DynamoDBLocal.jar",
				"-inMemory",
				"-disableTelemetry",
			},
			LogConsumerCfg: &testcontainers.LogConsumerConfig{
				Consumers: []testcontainers.LogConsumer{TestLogConsumer(t)},
			},
		},
		Started: true,
		Logger:  tclog.TestLogger(t),
	}

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, req)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	mappedPort, err := container.MappedPort(ctx, nat.Port(exposedPort))
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)

	return container, host, mappedPort.Port()
}
