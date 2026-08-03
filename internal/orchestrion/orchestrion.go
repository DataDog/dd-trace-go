// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import "github.com/DataDog/dd-trace-go/v2/internal/otelc"

// Orchestrion will change this at build-time
//
//orchestrion:enabled
var enabled = false

// The version of the orchestrion binary used to build the current binary, or
// blank if the current binary was not built using orchestrion.
//
//orchestrion:version
const Version = ""

// Enabled returns whether the current build was compiled with orchestrion or not.
//
// This reports the specific tool, so it stays false under otelc. Callers that
// only care whether the GLS was woven in want [glsActive] instead.
func Enabled() bool {
	return enabled
}

// glsActive reports whether this build has the goroutine-local storage woven in,
// under either orchestrion or otelc. Both tools inject the same runtime.g field
// and the same pair of linknamed accessors that gls.go consumes, so everything in
// this package works identically once either one is present.
//
// Kept separate from [Enabled] deliberately: Enabled also drives
// orchestrion-specific reporting (the orchestrion_enabled telemetry config), and
// making it true for otelc builds would misreport which tool was used.
func glsActive() bool {
	return enabled || otelc.Enabled()
}
