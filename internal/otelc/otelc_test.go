// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEnabled pins Enabled to the build-time flag rather than to a constant.
// It drives both values explicitly so the test means the same thing under a
// plain `go test` and under `otelc go test` (where the flag is already true).
// That the flag actually gets flipped is proven by
// internal/orchestrion/_integration/otelc, which only passes in an otelc build.
func TestEnabled(t *testing.T) {
	orig := enabled
	t.Cleanup(func() { enabled = orig })

	enabled = false
	assert.False(t, Enabled())

	enabled = true
	assert.True(t, Enabled())
}
