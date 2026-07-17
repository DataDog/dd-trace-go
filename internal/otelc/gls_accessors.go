// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build ignore
// +build ignore

package otelc

import _ "unsafe" // for go:linkname

// The symbol names below stay "orchestrion"-prefixed on purpose: they are a
// private go:linkname contract with internal/orchestrion/gls.go, which is
// shared unmodified between orchestrion and otelc builds.

//go:linkname __dd_orchestrion_gls_get __dd_orchestrion_gls_get.V2
var __dd_orchestrion_gls_get = func() any {
	return getg().m.curg.__dd_gls_v2
}

//go:linkname __dd_orchestrion_gls_set __dd_orchestrion_gls_set.V2
var __dd_orchestrion_gls_set = func(val any) {
	getg().m.curg.__dd_gls_v2 = val
}
