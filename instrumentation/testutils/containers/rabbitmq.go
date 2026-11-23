// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/wait"
)

func StartRabbitMQTestContainer(t testing.TB) (testcontainers.Container, string) {
	ctx := context.Background()
	const port = "5672/tcp"

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        Image("rabbitmq"),
			ExposedPorts: []string{port},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort(port),
				wait.ForLog("Server startup complete"),
			),
		},
		Started: true,
		Logger:  tclog.TestLogger(t),
	})
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, port)
	require.NoError(t, err)

	return container, fmt.Sprintf("amqp://guest:guest@%s:%s/", host, mappedPort.Port())
}
