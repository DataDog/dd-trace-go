// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"maps"
	"slices"
)

type libraryRegistry struct {
	checks map[string]func()
}

func newLibraryRegistry() *libraryRegistry {
	return &libraryRegistry{checks: map[string]func(){}}
}

func (r *libraryRegistry) register(name string, fn func()) {
	if name == "" {
		panic("registerLibrary: empty library name")
	}
	if fn == nil {
		panic("registerLibrary: nil check for " + name)
	}
	if _, exists := r.checks[name]; exists {
		panic("registerLibrary: duplicate library name " + name)
	}
	r.checks[name] = fn
}

func (r *libraryRegistry) run() {
	for _, name := range slices.Sorted(maps.Keys(r.checks)) {
		r.checks[name]()
	}
}

var defaultRegistry = newLibraryRegistry()

func registerLibrary(name string, fn func()) {
	defaultRegistry.register(name, fn)
}

// useLibraries calls libraries whose otelc rules need a contrib package.
// The app does not import those contrib packages. The otelc build fails if a
// rules module does not supply its contrib package. The integration suites
// cannot detect this failure because their module requires every contrib package.
//
// Register each new check in a new lib_<name>.go file, not in libraries.go.
func useLibraries() {
	defaultRegistry.run()
}
