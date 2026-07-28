// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

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

// StartAerospikeTestContainer starts a new Aerospike test container and returns
// the container and a "host:port" address string.
func StartAerospikeTestContainer(t testing.TB) (testcontainers.Container, string) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "aerospike:ce-7.2.0.6@sha256:79c30951edab8878b14b650f32db416106a21d458be994344e488756fe8b651f",
		ExposedPorts: []string{"3000/tcp"},
		WaitingFor:   wait.ForListeningPort("3000/tcp"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
		Logger:           tclog.TestLogger(t),
	})
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	host, err := container.Host(ctx)
	require.NoError(t, err)

	mappedPort, err := container.MappedPort(ctx, "3000/tcp")
	require.NoError(t, err)

	return container, fmt.Sprintf("%s:%s", host, mappedPort.Port())
}
