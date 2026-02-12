// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

// MockGLS sets up a mock GLS for testing and returns a cleanup function that
// restores the original state. It enables orchestrion and configures a fresh
// contextStack accessible via the standard getDDGLS/setDDGLS functions.
//
// This is intended for use by tests in packages that depend on orchestrion
// (e.g., internal, ddtrace/tracer). Follows the same pattern as
// telemetry.MockClient in internal/telemetry/globalclient.go.
//
// Tests using MockGLS must NOT use t.Parallel, as it mutates package-level
// variables without synchronization.
func MockGLS() func() {
	prevGetDDGLS := getDDGLS
	prevSetDDGLS := setDDGLS
	prevEnabled := enabled

	tmp := contextStack(make(map[any][]any))
	var glsValue any = &tmp
	getDDGLS = func() any { return glsValue }
	setDDGLS = func(a any) { glsValue = a }
	enabled = true

	return func() {
		getDDGLS = prevGetDDGLS
		setDDGLS = prevSetDDGLS
		enabled = prevEnabled
	}
}
