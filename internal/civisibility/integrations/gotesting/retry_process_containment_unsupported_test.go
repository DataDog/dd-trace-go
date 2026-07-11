// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build !unix && !windows

package gotesting

import "testing"

func requireProcessRetryContainmentForTesting(t testing.TB) {
	t.Helper()
	t.Skip("process retry fixture requires process-tree containment")
}

func processRetryContainmentAvailableForTesting() bool { return false }
