// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package gls

/*
extern void cgoCallback();
*/
import "C"

import (
	_ "runtime" // Provides go:linkname targets (if Orchestrion modifies)
	_ "unsafe"  // For go:linkname
)

var (
	//go:linkname orchestrionGlsGet __dd_orchestrion_gls_get
	orchestrionGlsGet func() any

	//go:linkname orhcestrionGlsSet __dd_orchestrion_gls_set
	orhcestrionGlsSet func(any)

	get = func() any { return nil }
	set = func(any) {}
)

func init() {
	if orchestrionGlsGet != nil {
		get = orchestrionGlsGet
	}
	if orhcestrionGlsSet != nil {
		set = orhcestrionGlsSet
	}
}

//export cgoCallback
func cgoCallback() {
	set("I am inside a cgo callback")
}

func cgoCall() {
	C.cgoCallback()
}
