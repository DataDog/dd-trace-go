package stackparse

import "bytes"

// Fuzz implements fuzzing using https://github.com/dvyukov/go-fuzz. See
// TestFuzzCorupus for generating an initial test corpus.
func Fuzz(data []byte) int {
	goroutines, _ := Parse(bytes.NewReader(data))
	if len(goroutines) > 0 {
		return 1
	}
	return 0
}
