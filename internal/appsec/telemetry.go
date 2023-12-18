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

func startTelemetry() {
	cfg := []telemetry.Configuration{
		makeTelemetryConfigEntry("goos", runtime.GOOS),
		makeTelemetryConfigEntry("goarch", runtime.GOARCH),
		makeTelemetryConfigEntry("cgo_enabled", strconv.FormatBool(cgoEnabled)),
	}
	if runtime.GOOS == "linux" {
		cfg = append(cfg, makeTelemetryConfigEntry("osinfo_libdl_path", osinfo.DetectLibDl("/")))
	}

	telemetry.GlobalClient.ProductChange(telemetry.NamespaceAppSec, true, cfg)
}

func makeTelemetryConfigEntry(name, value string) telemetry.Configuration {
	return telemetry.Configuration{
		Name:  name,
		Value: value,
	}
}
