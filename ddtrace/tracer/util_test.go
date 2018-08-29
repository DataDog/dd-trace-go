package tracer

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestParseUint64(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		id, err := parseUint64("-8809075535603237910")
		assert.NoError(t, err)
		assert.Equal(t, uint64(9637668538106313706), id)
	})
	t.Run("positive", func(t *testing.T) {
		id, err := parseUint64(fmt.Sprintf("%d", uint64(math.MaxUint64)))
		assert.NoError(t, err)
		assert.Equal(t, uint64(math.MaxUint64), id)
	})
	t.Run("invalid", func(t *testing.T) {
		_, err := parseUint64("abcd")
		assert.Error(t, err)
	})
}
