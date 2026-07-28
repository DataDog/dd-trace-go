// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestShouldDisableSpanPool covers the gate that makes the experimental span
// pool and Orchestrion's GLS weave mutually exclusive (orchestrion#782).
// orchestrion.Enabled() is a build-time constant (off under plain `go test`),
// so the decision is exercised here directly. The orchestrion-on path (pooling
// actually disabled) is covered by the woven gls integration tests; the
// orchestrion-off path (WithSpanPool(true) stays enabled, gate is a no-op) is
// covered by the rest of span_pool_test.go, which runs newConfig(WithSpanPool(true))
// and asserts pooling behaves.
func TestShouldDisableSpanPool(t *testing.T) {
	for _, tc := range []struct {
		name            string
		spanPoolEnabled bool
		orchestrionOn   bool
		want            bool
	}{
		{"both off", false, false, false},
		{"pool on, orchestrion off", true, false, false},
		{"pool off, orchestrion on", false, true, false},
		{"both on disables pool", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldDisableSpanPool(tc.spanPoolEnabled, tc.orchestrionOn))
		})
	}
}
