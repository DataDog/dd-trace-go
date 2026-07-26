// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

var configPackageInitState struct {
	reporterBindings []string
	rawIndexBefore   bool
	rawIndexAfter    bool
	definition       RawDefinition
	unknownPanic     any
}

func TestMain(m *testing.M) {
	instrumentationReporter.mu.Lock()
	bindings := make([]string, 0, len(instrumentationReporter.bindings))
	for id := range instrumentationReporter.bindings {
		bindings = append(bindings, id)
	}
	instrumentationReporter.mu.Unlock()
	sort.Strings(bindings)
	rawIndexBefore := instrumentationRawIndex.byKey != nil
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
		definition       RawDefinition
		unknownPanic     any
	}{
		reporterBindings: bindings,
		rawIndexBefore:   rawIndexBefore,
		rawIndexAfter:    instrumentationRawIndex.byKey != nil,
		definition:       definition,
		unknownPanic:     unknownPanic,
	}
	os.Exit(m.Run())
}

func TestConfigPackageInitRetainsOnlyAcceptedBindings(t *testing.T) {
	allowed := map[string]struct{}{
		"appsec.stacktrace-init": {},
		"system.app-endpoints":   {},
		"system.logging-rate":    {},
		"system.process-tags":    {},
	}
	for _, id := range configPackageInitState.reporterBindings {
		_, ok := allowed[id]
		require.Truef(t, ok, "package initialization retained unrelated binding %q", id)
	}
	require.LessOrEqual(t, len(configPackageInitState.reporterBindings), len(allowed))
	require.False(t, configPackageInitState.rawIndexBefore,
		"package initialization must not build the normal raw-definition index")
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
