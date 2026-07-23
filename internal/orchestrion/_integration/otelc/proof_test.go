// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestOtelcPresent proves the otelc build+weave+test pipeline works
// end-to-end: it only passes when this test binary was actually compiled
func TestOtelcPresent(t *testing.T) {
	if built.WithOrchestrion {
		t.Skip("this package is otelc-specific; built with orchestrion instead")
	}
	if !otelc.Enabled() {
		t.Fatal("this test was not built with otelc enabled")
	}
}
