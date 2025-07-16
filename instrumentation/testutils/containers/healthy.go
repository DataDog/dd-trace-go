// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package containers

import (
	"runtime"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
	"github.com/testcontainers/testcontainers-go"
)

// SkipIfProviderIsNotHealthy calls [testcontainers.SkipIfProviderIsNotHealthy] to skip tests of
// the testcontainers provider is not healthy or running at all; except when the test is running in
// CI mode (the CI environment variable is defined) and the GOOS is linux.
func SkipIfProviderIsNotHealthy(t *testing.T) {
	t.Helper()

	if _, ci := env.LookupEnv("CI"); ci && runtime.GOOS == "linux" {
		// We never want to skip tests on Linux CI, as this could lead to not noticing the tests are not
		// running at all, resulting in usurped confidence in the (un)tested code.
		return
	}

	defer func() {
		err := recover()
		if err == nil {
			return
		}
		// We recovered from a panic (e.g, "rootless Docker not found" on GitHub Actions + macOS), so we
		// will behave as if the provider was not healthy (because it's not and shouldn't have panic'd
		// in the first place).
		t.Log(err)
		t.SkipNow()
	}()

	testcontainers.SkipIfProviderIsNotHealthy(t)
}
