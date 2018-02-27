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
		0:  {1, 1, true},
		1:  {byte(1), 1, true},
		2:  {int(1), 1, true},
		3:  {int16(1), 1, true},
		4:  {int32(1), 1, true},
		5:  {int64(1), 1, true},
		6:  {uint(1), 1, true},
		7:  {uint16(1), 1, true},
		8:  {uint32(1), 1, true},
		9:  {uint64(1), 1, true},
		10: {"a", 0, false},
		11: {float32(1.25), 1.25, true},
		12: {float64(1.25), 1.25, true},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			f, ok := toFloat64(tt.value)
			if ok != tt.ok {
				t.Fatalf("expected ok: %t", tt.ok)
			}
			if f != tt.f {
				t.Fatalf("expected: %#v, got: %#v", tt.f, f)
			}
		})
	}
}
