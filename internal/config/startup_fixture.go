// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build config_startup_fixture

package config

import "sort"

// StartupState is exposed only to the config_startup_fixture build.
type StartupState struct {
	ReporterBindings        []string
	RawIndexInitialized     bool
	RegistryFrozen          bool
	RegistryValidationError string
}

// StartupStateForTesting returns retained configuration startup state.
func StartupStateForTesting() StartupState {
	instrumentationReporter.mu.Lock()
	bindings := make([]string, 0, len(instrumentationReporter.bindings))
	for id := range instrumentationReporter.bindings {
		bindings = append(bindings, id)
	}
	instrumentationReporter.mu.Unlock()
	sort.Strings(bindings)

	var validationError string
	if definitionsRegistry.freezeErr != nil {
		validationError = definitionsRegistry.freezeErr.Error()
	}
	return StartupState{
		ReporterBindings:        bindings,
		RawIndexInitialized:     instrumentationRawIndex.byKey != nil,
		RegistryFrozen:          definitionsRegistry.frozen.Load(),
		RegistryValidationError: validationError,
	}
}
