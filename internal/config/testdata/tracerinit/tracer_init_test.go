// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracerinit

import (
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestTracerPackageInitReportsImmediatelyWithoutFullIndexes(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	telemetry.StartApp(recorder)
	t.Cleanup(telemetry.StopApp)

	var packageInitReports []string
	for _, configuration := range recorder.Configuration {
		switch configuration.Name {
		case "DD_APPSEC_SCA_ENABLED",
			"DD_APPSEC_AGENTIC_ONBOARDING",
			"DD_TRACE_DEBUG_SEELOG_WORKAROUND",
			"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED":
			packageInitReports = append(packageInitReports,
				configuration.Name+"@"+string(configuration.Origin))
		}
	}
	require.Equal(t, []string{
		"DD_APPSEC_SCA_ENABLED@env_var",
		"DD_APPSEC_AGENTIC_ONBOARDING@env_var",
		"DD_TRACE_DEBUG_SEELOG_WORKAROUND@env_var",
		"DD_TRACE_DEBUG_SEELOG_WORKAROUND@default",
		"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED@env_var",
		"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED@default",
	}, packageInitReports, "package-init reports must already be queued in import order when telemetry starts")

	state := internalconfig.StartupStateForTesting()
	require.Equal(t, []string{
		"appsec.agentic-init-telemetry",
		"appsec.sca-init-telemetry",
		"appsec.stacktrace-init",
		"system.app-endpoints",
		"system.logging-rate",
		"system.process-tags",
		"tracer.seelog-init",
		"tracer.trace-id-generation-init",
	}, state.ReporterBindings)
	require.True(t, state.RegistryFrozen)
	require.Empty(t, state.RegistryValidationError)
	require.False(t, state.RawIndexInitialized)
}
