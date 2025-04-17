// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import (
	"os"
	"testing"

	"github.com/zeebo/assert"
)

const (
	simpleInvalidYaml = `
a: Easy!
b:
  c: 2
  d: [3, 4]
`

	simpleValidYaml = `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: value_1
    "DD_KEY_2": "value_2"

`

	simpleEmptyYaml = ``
)

func TestFileContentsToConfig(t *testing.T) {
	t.Run("simple failure", func(t *testing.T) {
		scfg := fileContentsToConfig([]byte(simpleInvalidYaml), "test.yml")
		assert.True(t, scfg.isEmpty())
	})
	t.Run("simple success", func(t *testing.T) {
		scfg := fileContentsToConfig([]byte(simpleValidYaml), "test.yml")
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
		scfg := fileContentsToConfig([]byte(simpleEmptyYaml), "test.yml")
		assert.True(t, scfg.isEmpty())
	})
}

func TestParseFile(t *testing.T) {
	t.Run("file doesn't exist", func(t *testing.T) {
		scfg := ParseFile("test.yml")
		assert.True(t, scfg.isEmpty())
	})
	t.Run("success", func(t *testing.T) {
		err := os.WriteFile("test.yml", []byte(simpleValidYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test.yml")
		scfg := ParseFile("test.yml")
		assert.Equal(t, scfg.Id, 67890)
		assert.Equal(t, len(scfg.Config), 2)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "value_1")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "value_2")
	})
}
