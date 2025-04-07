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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

// RegisterContainerCleanup registers a function to terminate the provided container to be executed after the test finishes.
func RegisterContainerCleanup(t testing.TB, container testcontainers.Container) {
	t.Cleanup(func() {
		if _, ok := os.LookupEnv("CI"); ok {
			t.Log("skipping container cleanup in CI environment")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		assert.NoError(t, container.Terminate(ctx))
	})
}
