// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package profiler

import (
	"bytes"
	"compress/gzip"
	"io"

	"github.com/ianlancetaylor/demangle"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/pproflite"
)

// demangleCPUProfile demangles C++ or Rust symbol names appearing in the string
// table for the given profile, and returns a new profile.
func demangleCPUProfile(p []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		return nil, err
	}
	p, err = io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	out := new(bytes.Buffer)
	w := gzip.NewWriter(out)
	enc := pproflite.NewEncoder(w)

	err = pproflite.NewDecoder(p).FieldEach(func(field pproflite.Field) error {
		switch t := field.(type) {
		case *pproflite.StringTable:
			if maybeMangled(t.Value) {
				demangled := demangle.Filter(string(t.Value))
				t.Value = []byte(demangled)
			}
		}
		return enc.Encode(field)
	})
	w.Close()

	return out.Bytes(), err
}

// manglePrefixes are the prefixes that github.com/ianlancetaylor/demangle checks
// to see if a symbol name might be mangled
var manglePrefixes = [][]byte{
	[]byte(`_R`),   // Rust
	[]byte(`_Z`),   // Itanium
	[]byte(`___Z`), // Clang extensions
	[]byte(`_GLOBAL_`),
}

// maybeMangled returns whether b might be a mangled symbol, based on its prefix
func maybeMangled(b []byte) bool {
	for _, p := range manglePrefixes {
		if bytes.HasPrefix(b, p) {
			return true
		}
	}
	return false
}
