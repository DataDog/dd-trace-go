// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import _ "unsafe" // for go:linkname

var (
	// getDDGLS returns the current value from the field inserted in runtime.g by orchestrion.
	// Or nil if orchestrion is not enabled.
	getDDGLS = func() any { return nil }
	// setDDGLS sets the value in the field inserted in runtime.g by orchestrion.
	// Or does nothing if orchestrion is not enabled.
	setDDGLS = func(any) {}
)

//go:linkname _DdOrchestrionGlsGet __dd_orchestrion_gls_get
var _DdOrchestrionGlsGet func() any

//go:linkname _DdOrchestrionGlsSet __dd_orchestrion_gls_set
var _DdOrchestrionGlsSet func(any)

// Check at Go init time that the two function variable values created by the
// orchestrion are present, and set the get/set variables to their
// values.
func init() {
	if _DdOrchestrionGlsGet != nil && _DdOrchestrionGlsSet != nil {
		getDDGLS = _DdOrchestrionGlsGet
		setDDGLS = _DdOrchestrionGlsSet
	}
}
