// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cassandra"
)

// StartCassandraContainer starts a new Cassandra test container and returns the host and port.
func StartCassandraContainer(t testing.TB) (*cassandra.CassandraContainer, string, string) {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse cassandra container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "cassandra",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := cassandra.Run(ctx,
		"cassandra:4.1", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	hostPort, err := container.ConnectionHost(ctx)
	require.NoError(t, err)

	host, port, err := net.SplitHostPort(hostPort)
	require.NoError(t, err)

	return container, host, port
}
