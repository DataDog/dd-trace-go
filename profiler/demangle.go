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
	defer w.Close()
	enc := pproflite.NewEncoder(w)

	pproflite.NewDecoder(p).FieldEach(func(field pproflite.Field) error {
		switch t := field.(type) {
		case *pproflite.StringTable:
			demangled := demangle.Filter(string(t.Value))
			v := pproflite.StringTable{Value: []byte(demangled)}
			enc.Encode(v)
		default:
			enc.Encode(field)
		}
		return nil
	})

	return out.Bytes(), nil
}
