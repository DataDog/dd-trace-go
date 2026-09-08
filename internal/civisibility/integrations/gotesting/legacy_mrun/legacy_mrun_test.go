// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package legacymrun

import (
	"os"
	"testing"
	_ "unsafe"

	_ "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

// This declaration matches the testing.M advice published by the v1
// CI Visibility Orchestrion bridge.
//
//go:linkname legacyInstrumentTestingM github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingM
func legacyInstrumentTestingM(*testing.M) func(int)

func TestMain(m *testing.M) {
	_ = os.Setenv("DD_CIVISIBILITY_ENABLED", "false")
	finalize := legacyInstrumentTestingM(m)
	exitCode := m.Run()
	finalize(exitCode)
	os.Exit(exitCode)
}

func TestLegacyTestingMAdviceContract(*testing.T) {}
