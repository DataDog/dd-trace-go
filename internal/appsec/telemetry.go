// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"runtime"
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// cgoEnabled is true if cgo is enabled, false otherwise.
// No way to check this at runtime, so we compute it at build time in
// telemetry_cgo.go.
var cgoEnabled bool

// reportToTelemetry emits the relevant telemetry product change event to report
// activation of the AppSec feature.
func reportToTelemetry() {
	if telemetry.Disabled() {
		// Do nothing if telemetry is explicitly disabled.
		return
	}

	cfg := []telemetry.Configuration{
		{Name: "goos", Value: runtime.GOOS},
		{Name: "goarch", Value: runtime.GOARCH},
		{Name: "cgo_enabled", Value: strconv.FormatBool(cgoEnabled)},
	}
	if runtime.GOOS == "linux" {
		cfg = append(cfg, telemetry.Configuration{Name: "osinfo_libdl_path", Value: osinfo.DetectLibDl("/")})
	}

	telemetry.GlobalClient.ProductChange(telemetry.NamespaceAppSec, true, cfg)
}
