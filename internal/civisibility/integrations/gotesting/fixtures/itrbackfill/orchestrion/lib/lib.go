// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package lib

import "testing"

// Answer returns a stable value used by the Orchestrion ITR backfill fixture.
func Answer() int {
	value := 41
	value++
	return value
}

// RunSubtest creates a subtest from production code so the Orchestrion fixture
// can verify that its module stays associated with the consuming test binary.
func RunSubtest(t *testing.T) {
	t.Helper()
	t.Run("from-production-helper", func(t *testing.T) {
		t.Helper()
	})
}
