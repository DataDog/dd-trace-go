package tracer

import (
	"fmt"
	"testing"
)

func TestToFloat64(t *testing.T) {
	for i, tt := range [...]struct {
		value interface{}
		f     float64
		ok    bool
	}{
		0: {1, 1, true},
		1: {uint16(1), 1, true},
		2: {uint32(1), 1, true},
		3: {"a", 0, false},
		4: {float64(1.25), 1.25, true},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			f, ok := toFloat64(tt.value)
			if ok != tt.ok {
				t.Fatalf("expected ok: %B", tt.ok)
			}
			if f != tt.f {
				t.Fatalf("expected: %#v, got: %#v", tt.f, f)
			}
		})
	}
}
