// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import (
	"testing"

	"github.com/zeebo/assert"
)

func TestFileContentsToConfig(t *testing.T) {
	t.Run("simple failure", func(t *testing.T) {
		data := `
		a: Easy!
		b:
		  c: 2
		  d: [3, 4]
		`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.DeepEqual(t, scfg, emptyStableConfig())
	})
	t.Run("simple success", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: value_1
    "DD_KEY_2": "value_2"

`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, scfg.Id, 67890)
		assert.Equal(t, len(scfg.Config), 2)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "value_1")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "value_2")
	})
	t.Run("success without apm_configuration_default", func(t *testing.T) {
		data := `
config_id: 67890
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, len(scfg.Config), 0)
		assert.Equal(t, scfg.Id, 67890)
	})
	t.Run("success without config_id", func(t *testing.T) {
		data := `
apm_configuration_default:
    DD_KEY_1: value_1
    "DD_KEY_2": "value_2"

`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, len(scfg.Config), 2)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "value_1")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "value_2")
		assert.Equal(t, scfg.Id, -1)
	})
	t.Run("success with empty contents", func(t *testing.T) {
		data := ``
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.DeepEqual(t, scfg, emptyStableConfig())
	})
}
