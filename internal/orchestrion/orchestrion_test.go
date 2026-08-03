// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package orchestrion

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestGLSActive pins the split between the two predicates: Enabled names the
// specific tool (it feeds the orchestrion_enabled telemetry config, so it must
// stay false under otelc), while glsActive answers the question every gate in
// this package actually asks, which is whether the GLS was woven in at all.
//
// Only the orchestrion input is settable from here, since otelc's flag lives in
// another package. The otelc side is covered end to end by
// internal/orchestrion/_integration/gls under `otelc go test`.
func TestGLSActive(t *testing.T) {
	orig := enabled
	t.Cleanup(func() { enabled = orig })

	enabled = true
	assert.True(t, glsActive(), "an orchestrion build must have the GLS active")
	assert.True(t, Enabled(), "Enabled must report orchestrion specifically")

	enabled = false
	// Comparing against otelc.Enabled() rather than asserting false keeps this
	// true under both `go test` (otelc absent, gate closed) and `otelc go test`
	// (otelc present, gate open).
	assert.Equal(t, otelc.Enabled(), glsActive(),
		"with orchestrion off, glsActive must follow otelc alone")
	assert.False(t, Enabled(), "Enabled must not be affected by otelc")
}
