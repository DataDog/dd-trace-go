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

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/vault"
)

// StartVaultContainer starts a new Vault test container.
func StartVaultContainer(t testing.TB) *vault.VaultContainer {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
		vault.WithToken("root"),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse vault container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "vault",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := vault.Run(ctx,
		"vault:1.7.3", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	return container
}
