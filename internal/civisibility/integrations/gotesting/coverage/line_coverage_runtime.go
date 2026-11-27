// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	_ "runtime" // defines the symbols below in test binaries built with line coverage
	_ "unsafe"  // for go:linkname
)

// Accessors that orchestrion adds to the runtime when it builds a test binary
// with line coverage. They are nil in any other binary, and in binaries built
// by orchestrion versions whose slots have a single row. Every goroutine carries
// a coverage slot, inherited from the goroutine that starts it, and a lane of its
// own; the probes of repository code mark blocks in the row of the running
// goroutine's lane of its slot.

// runtimeLineCoverageSlot returns the slot of the calling goroutine.
//
//go:linkname runtimeLineCoverageSlot __dd_linecov_slot.V2
var runtimeLineCoverageSlot func() uint32

// runtimeLineCoverageSetSlot sets the slot of the calling goroutine, and
// returns its previous slot.
//
//go:linkname runtimeLineCoverageSetSlot __dd_linecov_set_slot.V2
var runtimeLineCoverageSetSlot func(slot uint32) uint32

// runtimeLineCoverageSlotRefs returns the number of live goroutines that carry
// slot.
//
//go:linkname runtimeLineCoverageSlotRefs __dd_linecov_refs.V2
var runtimeLineCoverageSlotRefs func(slot uint32) int32

// runtimeLineCoveragePackage returns the counters of the i-th instrumented
// package: lineCoverageLaneCount rows of rowSize bytes per slot, with one
// counter per block, and
// the encoded table of its blocks, which gives the counter size. ok is false
// past the last package.
//
//go:linkname runtimeLineCoveragePackage __dd_linecov_package.V1
var runtimeLineCoveragePackage func(i int) (counters *byte, numBlocks int, rowSize int, pkgPath string, meta string, ok bool)

// runtimeLineCoverageLines reports the lines the line table of the binary maps
// code to, in the files wantFile accepts: for each run of instructions of a
// function that map to one line, it calls line with the name of the function,
// the file and the line, and whether the instructions come from a function
// inlined into it. It also reports the header line of each function that
// inlines calls other than those of probes: without inlining, such a function
// has a frame, and its prologue maps to the header. Probes map to no file of
// the repository. It is nil in test binaries built by orchestrion versions
// that predate it.
//
//go:linkname runtimeLineCoverageLines __dd_linecov_lines.V1
var runtimeLineCoverageLines func(wantFile func(file string) bool, line func(funcName string, file string, line int32, inlined bool))

// lineCoverageRuntimeAvailable reports whether the runtime has the line
// coverage accessors.
func lineCoverageRuntimeAvailable() bool {
	return runtimeLineCoverageSlot != nil &&
		runtimeLineCoverageSetSlot != nil &&
		runtimeLineCoverageSlotRefs != nil &&
		runtimeLineCoveragePackage != nil
}
