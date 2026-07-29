// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var configPackageInitState struct {
	reporterBindings []string
	rawIndexBefore   bool
	rawIndexAfter    bool
	registryFrozen   bool
	registryError    error
	definition       RawDefinition
	unknownPanic     any
}

const configStartupStateChild = "DD_CONFIG_STARTUP_STATE_CHILD"

func TestMain(m *testing.M) {
	instrumentationReporter.mu.Lock()
	bindings := make([]string, 0, len(instrumentationReporter.bindings))
	for id := range instrumentationReporter.bindings {
		bindings = append(bindings, id)
	}
	instrumentationReporter.mu.Unlock()
	sort.Strings(bindings)
	rawIndexBefore := instrumentationRawIndex.byKey != nil
	registryFrozen := definitionsRegistry.frozen.Load()
	registryError := definitionsRegistry.freezeErr
	definition := registeredDefinitionForInit("DD_SERVICE")
	var unknownPanic any
	func() {
		defer func() {
			unknownPanic = recover()
		}()
		registeredDefinitionForInit("DD_UNKNOWN_INIT_DEFINITION")
	}()
	configPackageInitState = struct {
		reporterBindings []string
		rawIndexBefore   bool
		rawIndexAfter    bool
		registryFrozen   bool
		registryError    error
		definition       RawDefinition
		unknownPanic     any
	}{
		reporterBindings: bindings,
		rawIndexBefore:   rawIndexBefore,
		rawIndexAfter:    instrumentationRawIndex.byKey != nil,
		registryFrozen:   registryFrozen,
		registryError:    registryError,
		definition:       definition,
		unknownPanic:     unknownPanic,
	}
	os.Exit(m.Run())
}

func TestConfigPackageInitRetainsOnlyAcceptedBindings(t *testing.T) {
	if os.Getenv(configStartupStateChild) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestConfigPackageInitRetainsOnlyAcceptedBindings$", "-test.count=1")
		cmd.Env = sanitizedConfigStartupEnvironment(os.Environ())
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	require.Equal(t, []string{
		"appsec.stacktrace-init",
		"system.app-endpoints",
		"system.logging-rate",
		"system.process-tags",
	}, configPackageInitState.reporterBindings)
	require.False(t, configPackageInitState.rawIndexBefore,
		"package initialization must not build the normal raw-definition index")
	require.True(t, configPackageInitState.registryFrozen,
		"the complete registry must be validated and frozen during package initialization")
	require.NoError(t, configPackageInitState.registryError)
}

func TestInitScopedDefinitionLookupDoesNotBuildRuntimeIndex(t *testing.T) {
	require.Equal(t, RawDefinition{
		Key:       "DD_SERVICE",
		Sources:   SourceStable,
		Telemetry: TelemetryReport,
	}, configPackageInitState.definition)
	require.Equal(t,
		"config definition not registered: DD_UNKNOWN_INIT_DEFINITION",
		configPackageInitState.unknownPanic,
	)
	require.False(t, configPackageInitState.rawIndexAfter)
	require.Zero(t, testing.AllocsPerRun(100, func() {
		_ = registeredDefinitionForInit("DD_SERVICE")
	}))
}

func TestDownstreamTracerPackageInitFixture(t *testing.T) {
	cmd := exec.Command(
		"go", "test",
		"-tags=config_startup_fixture",
		"./testdata/tracerinit",
		"-count=1",
	)
	cmd.Env = append(sanitizedConfigStartupEnvironment(os.Environ()),
		"DD_APPSEC_SCA_ENABLED=true",
		"DD_APPSEC_AGENTIC_ONBOARDING=fixture",
		"DD_TRACE_DEBUG_SEELOG_WORKAROUND=false",
		"DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED=false",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

func sanitizedConfigStartupEnvironment(environment []string) []string {
	sanitized := make([]string, 0, len(environment)+2)
	for _, variable := range environment {
		name, _, _ := strings.Cut(variable, "=")
		if strings.HasPrefix(name, "DD_") || strings.HasPrefix(name, "OTEL_") {
			continue
		}
		sanitized = append(sanitized, variable)
	}
	return append(sanitized,
		configStartupStateChild+"=1",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=true",
	)
}
