// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"runtime"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/stretchr/testify/require"
)

// AssertTestContainersError decides whether the provided testcontainers error should make the test
// fail or mark it as skipped, depending on the environment where the test is running.
func AssertTestContainersError(t testing.TB, err error) {
	if err == nil {
		return
	}
	if _, ok := env.LookupEnv("CI"); ok && runtime.GOOS != "linux" {
		t.Skipf("failed to start container (CI does not support docker, skipping test): %s", err.Error())
		return
	}
	require.NoError(t, err)
}
