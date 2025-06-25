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
	tclog "github.com/testcontainers/testcontainers-go/log"
	testvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartValkeyTestContainer starts a new Valkey test container and returns the connection string.
func StartValkeyTestContainer(t testing.TB) (*testvalkey.ValkeyContainer, string) {
	ctx := context.Background()
	exposedPort := "6379/tcp"
	waitReadyCmd := []string{
		"valkey-cli",
		"ping",
	}

	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(tclog.TestLogger(t)),
		WithTestLogConsumer(t),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("* Ready to accept connections"),
				wait.ForExposedPort(),
				wait.ForListeningPort(nat.Port(exposedPort)),
				wait.ForExec(waitReadyCmd),
			),
		),
		// attempt to reuse this container
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "valkey",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}),
	}

	container, err := testvalkey.Run(ctx, "valkey/valkey:8-alpine", opts...)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	connStr, err := container.ConnectionString(ctx)
	require.NoError(t, err)

	valkeyURL, err := url.Parse(connStr)
	require.NoError(t, err)

	return container, valkeyURL.Host
}
