// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
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
