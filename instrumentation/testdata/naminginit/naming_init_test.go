// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package naminginit

import (
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/DataDog/dd-trace-go/v2/instrumentation/internal/namingschema"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestInstrumentationNamingPackageInitReportsWithoutFullIndexes(t *testing.T) {
	recorder := new(telemetrytest.RecordClient)
	telemetry.StartApp(recorder)
	t.Cleanup(telemetry.StopApp)

	var namingReports []string
	for _, configuration := range recorder.Configuration {
		switch configuration.Name {
		case "DD_TRACE_SPAN_ATTRIBUTE_SCHEMA",
			"DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED":
			namingReports = append(namingReports,
				configuration.Name+"@"+string(configuration.Origin))
		}
	}
	require.Equal(t, []string{
		"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA@env_var",
		"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA@default",
		"DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED@env_var",
		"DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED@default",
	}, namingReports)

	state := internalconfig.StartupStateForTesting()
	require.Equal(t, []string{
		"appsec.stacktrace-init",
		"instrumentation.naming-init",
		"system.app-endpoints",
		"system.logging-rate",
		"system.process-tags",
	}, state.ReporterBindings)
	require.True(t, state.RegistryFrozen)
	require.Empty(t, state.RegistryValidationError)
	require.False(t, state.RawIndexInitialized)
}
