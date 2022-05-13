// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package cmemprof

import (
	"debug/elf"
	"debug/macho"
	"os"
	"runtime"
	"sort"
	"strings"
)

/*
#include <stdint.h> // for uintptr_t

#include "profiler.h"
*/
import "C"

// withPrefix adds a "_" to the beginning of a symbol name for darwin
func withPrefix(name string) string {
	if runtime.GOOS == "darwin" {
		return "_" + name
	}
	return name
}

// isUnsafeMallocCall returns true if the given name identifies a function which
// calls malloc in a way that makes it unsafe to profile (by calling back into
// Go without certain invariants holding)
func isUnsafeMallocCall(name string) bool {
	// Normally, when cgo creates a Go function wrapper to call a C
	// function, the return values for the C function end up on the calling
	// goroutine's stack. Since goroutine's stacks can grow, a check is
	// added for the address where the return value should go to adjust it
	// if the C function called back into Go and caused the goroutine's
	// stack to grow and move.
	//
	// However, cgo generates a speciall wrapper for malloc for use by a few
	// other special cgo functions. The cgo-generated code omits the stack
	// move checks because it's assumed that the wrapper will not call back
	// into Go (see
	// https://go-review.googlesource.com/c/go/+/23260/1..4/src/cmd/cgo/out.go#b1476).
	// As a result, if the profiling function causes the stack to grow then
	// the return value of malloc will go to the wrong place.
	//
	// Each package that calls C.malloc will get its own wrapper,
	// and the symbol will look something like this:
	//
	//	_cgo_fae4bac1ae99_Cfunc__Cmalloc
	//
	if strings.HasSuffix(name, "_Cfunc__Cmalloc") && strings.HasPrefix(name, withPrefix("_cgo")) {
		return true
	}

	// When a cgo program creates a new OS thread (to back Go runtime "m"),
	// the x_cgo_thread_start function sets up some thread data to pass to
	// pthread_create. This data is allocated through malloc. At this point,
	// it is not safe to call back into Go as the "m" does not have an
	// associated goroutine to run Go code. We can't profile in this case or
	// the program will crash by dereferencing a NULL goroutine pointer.
	if name == withPrefix("x_cgo_thread_start") {
		return true
	}
	return false
}

func findUnsafeMallocPointsELF(f *elf.File) {
	symbols, err := f.DynamicSymbols()
	if err != nil {
		return
	}
	for _, sym := range symbols {
		if isUnsafeMallocCall(sym.Name) {
			C.cgo_heap_profiler_malloc_mark_unsafe(
				C.uintptr_t(sym.Value),
				C.uintptr_t(sym.Value+sym.Size),
			)
		}
	}
}

func findUnsafeMallocPointsMachO(f *macho.File) {
	symbols := f.Symtab.Syms
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Value < symbols[j].Value
	})
	for i, sym := range symbols {
		if isUnsafeMallocCall(sym.Name) && i+1 < len(symbols) {
			// The symbols in the table only have the starting
			// address, but the start of the next symbol should be
			// one past the end of this one
			//
			// TODO: what if this is the last symbol?
			C.cgo_heap_profiler_malloc_mark_unsafe(
				C.uintptr_t(sym.Value),
				C.uintptr_t(symbols[i+1].Value),
			)
		}
	}
}

func init() {
	if f, err := elf.Open(os.Args[0]); err == nil {
		defer f.Close()
		findUnsafeMallocPointsELF(f)
		return
	}
	if f, err := macho.Open(os.Args[0]); err == nil {
		defer f.Close()
		findUnsafeMallocPointsMachO(f)
		return
	}
}
