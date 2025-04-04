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
	"github.com/testcontainers/testcontainers-go/modules/gcloud"
)

// StartGCPPubsubContainer starts a new GCP Pubsub test container.
func StartGCPPubsubContainer(t testing.TB) *gcloud.GCloudContainer {
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		gcloud.WithProjectID("pstest"),
		testcontainers.WithLogger(testcontainers.TestLogger(t)),
		WithTestLogConsumer(t),
	}
	if _, ok := os.LookupEnv("CI"); ok {
		t.Log("attempting to reuse gcp-pubsub container in CI")
		opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Name:     "gcp-pubsub",
				Hostname: "localhost",
			},
			Started: true,
			Reuse:   true,
		}))
	}

	container, err := gcloud.RunPubsub(ctx,
		"gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators", // Change the docker pull stage in .github/workflows/orchestrion.yml if you update this
		opts...,
	)
	AssertTestContainersError(t, err)
	RegisterContainerCleanup(t, container)

	return container
}
