// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	testopensearch "github.com/testcontainers/testcontainers-go/modules/opensearch"
)

// StartOpenSearchTestContainer starts a new OpenSearch test container and returns its HTTP address.
func StartOpenSearchTestContainer(t testing.TB) (*testopensearch.OpenSearchContainer, string) {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(tclog.TestLogger(t)),
		WithTestLogConsumer(t),
		// attempt to reuse this container
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "opensearch2",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}),
	}

	container, err := testopensearch.Run(ctx, Image("opensearch2"), opts...)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	addr, err := container.Address(ctx)
	require.NoError(t, err)

	return container, addr
}
