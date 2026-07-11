// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !unix && !windows

package retryprocess

import "testing"

func skipProcessRetryFixtureChildLaunchIneligible(t *testing.T, name string) {
	t.Helper()
	t.Skipf("process retry %s fixture requires process-tree containment", name)
}
