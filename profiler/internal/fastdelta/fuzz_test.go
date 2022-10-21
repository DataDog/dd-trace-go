//go:build go1.18

package fastdelta_test

import (
	"io"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/fastdelta"
)

// FuzzDelta looks for inputs to delta which cause crashes. This is to account
// for the possibility that the profile format changes in some way, or violates
// any hard-coded assumptions.
func FuzzDelta(f *testing.F) {
	f.Fuzz(func(t *testing.T, b []byte) {
		dc := fastdelta.NewDeltaComputer()
		dc.Delta(b, io.Discard)
	})
}
