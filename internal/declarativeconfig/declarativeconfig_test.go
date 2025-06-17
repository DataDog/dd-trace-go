package declarativeconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	dc := declarativeConfigMap(map[string]any{
		"string": "string_value",
		"bool":   true,
	})
	t.Run("string", func(t *testing.T) {
		v, ok := dc.getString("string")
		assert.True(t, ok)
		assert.Equal(t, "string_value", v)

		_, ok = dc.getString("bool")
		assert.False(t, ok)
	})
	t.Run("bool", func(t *testing.T) {
		v, ok := dc.getBool("bool")
		assert.True(t, ok)
		assert.Equal(t, true, v)

		_, ok = dc.getBool("string")
		assert.False(t, ok)
	})
}
