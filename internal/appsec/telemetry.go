// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec

import (
	"runtime"

	waf "github.com/DataDog/go-libddwaf/v3"

	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

var (
	wafSupported, _ = waf.SupportsTarget()
	wafHealthy, _   = waf.Health()
	staticConfigs   = []telemetry.Configuration{
		{Name: "goos", Value: runtime.GOOS, Origin: telemetry.OriginCode},
		{Name: "goarch", Value: runtime.GOARCH, Origin: telemetry.OriginCode},
		{Name: "cgo_enabled", Value: cgoEnabled, Origin: telemetry.OriginCode},
		{Name: "waf_supports_target", Value: wafSupported, Origin: telemetry.OriginCode},
		{Name: "waf_healthy", Value: wafHealthy, Origin: telemetry.OriginCode},
	}
)

// init sends the static telemetry for AppSec.
func init() {
	telemetry.RegisterAppConfigs(staticConfigs...)
}

func registerAppsecStartTelemetry(mode config.EnablementMode, origin telemetry.Origin) {
	if mode == config.RCStandby {
		return
	}

	if origin == telemetry.OriginCode {
		telemetry.RegisterAppConfig("WithEnablementMode", mode, telemetry.OriginCode)
	}

	telemetry.ProductStarted(telemetry.NamespaceAppSec)
	telemetry.RegisterAppConfig("DD_APPSEC_ENABLED", mode == config.ForcedOn, origin)
	// TODO: add appsec.enabled metric once this metric is enabled backend-side
}

func registerAppsecStopTelemetry() {
	telemetry.ProductStopped(telemetry.NamespaceAppSec)
}
