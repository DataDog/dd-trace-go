// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build ignore

// This file is never compiled as part of this package. The add_file rule in
// otelc.yaml copies it into the runtime package during an otelc build, where
// getg() and the injected g.__dd_gls_v2 field exist. otelc rewrites the package
// clause and drops the build constraint on the way in.
//
// The "orchestrion" prefix on the symbols is load-bearing: they are linked
// against the matching //go:linkname vars in internal/orchestrion/gls.go, which
// is shared unmodified between orchestrion and otelc builds. Renaming either
// side silently breaks the link, leaving the GLS permanently empty.
package tracer

import _ "unsafe" // for go:linkname

// Reference for why the accessors go through m.curg rather than getg() directly:
// https://github.com/golang/go/blob/6d89b38ed86e0bfa0ddaba08dc4071e6bb300eea/src/runtime/HACKING.md?plain=1#L44-L54

//revive:disable:var-naming

//go:linkname __dd_orchestrion_gls_get __dd_orchestrion_gls_get.V2
var __dd_orchestrion_gls_get = func() any {
	return getg().m.curg.__dd_gls_v2
}

//go:linkname __dd_orchestrion_gls_set __dd_orchestrion_gls_set.V2
var __dd_orchestrion_gls_set = func(val any) {
	getg().m.curg.__dd_gls_v2 = val
}

//revive:enable:var-naming
