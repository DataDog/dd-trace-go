// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
)

// RegisterContainerCleanup registers a function to terminate the provided container to be executed after the test finishes.
func RegisterContainerCleanup(t testing.TB, container testcontainers.Container) {
	t.Cleanup(func() {
		// Always stop log production to prevent goroutines from logging after the test completes
		if err := container.StopLogProducer(); err != nil {
			t.Logf("failed to stop log producer: %v", err)
		}

		// In CI, we skip container termination to allow container reuse
		if _, ok := env.Lookup("CI"); ok {
			t.Log("skipping container cleanup in CI environment")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		assert.NoError(t, container.Terminate(ctx))
	})
}
