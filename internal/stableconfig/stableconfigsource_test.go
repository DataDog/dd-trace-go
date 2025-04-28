// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package stableconfig provides utilities to load and manage APM configurations
// loaded from YAML configuration files
package stableconfig

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	validYaml = `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: value_1
    "DD_KEY_2": "value_2"
`
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
		assert.True(t, scfg.isEmpty())
	})
	t.Run("simple success", func(t *testing.T) {
		scfg := fileContentsToConfig([]byte(validYaml), "test.yml")
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
		scfg := fileContentsToConfig([]byte(``), "test.yml")
		assert.True(t, scfg.isEmpty())
	})

	t.Run("numeric values", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: 123
    DD_KEY_2: 3.14
    DD_KEY_3: -42
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, scfg.Id, 67890)
		assert.Equal(t, len(scfg.Config), 3)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "123")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "3.14")
		assert.Equal(t, scfg.Config["DD_KEY_3"], "-42")
	})

	t.Run("boolean values", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: true
    DD_KEY_2: false
    DD_KEY_3: yes
    DD_KEY_4: no
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, scfg.Id, 67890)
		assert.Equal(t, len(scfg.Config), 4)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "true")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "false")
		assert.Equal(t, scfg.Config["DD_KEY_3"], "yes")
		assert.Equal(t, scfg.Config["DD_KEY_4"], "no")
	})

	t.Run("malformed YAML - missing colon", func(t *testing.T) {
		data := `
config_id 67890
apm_configuration_default
    DD_KEY_1 value_1
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, scfg.isEmpty())
	})

	t.Run("malformed YAML - incorrect indentation", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
DD_KEY_1: value_1
  DD_KEY_2: value_2
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, scfg.isEmpty())
	})

	t.Run("malformed YAML - duplicate keys", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: value_1
    DD_KEY_1: value_2
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, scfg.isEmpty()) // yaml.v3 treats duplicate keys as an error
	})

	t.Run("malformed YAML - unclosed quotes", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: "value_1
    DD_KEY_2: value_2
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, scfg.isEmpty())
	})

	t.Run("malformed YAML - special characters in keys", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY@1: value_1
    DD_KEY#2: value_2
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, scfg.isEmpty())
	})

	t.Run("malformed YAML - invalid nested structure", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    nested:
        - item1
        - item2
    DD_KEY_1: value_1
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, scfg.isEmpty())
	})

	t.Run("malformed YAML - special characters in values", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: "value with spaces"
    DD_KEY_2: "value with \n newline"
    DD_KEY_3: "value with \t tab"
    DD_KEY_4: "value with \" quotes"
    DD_KEY_5: "value with \\ backslash"
`
		scfg := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, scfg.Id, 67890)
		assert.Equal(t, len(scfg.Config), 5)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "value with spaces")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "value with \n newline")
		assert.Equal(t, scfg.Config["DD_KEY_3"], "value with \t tab")
		assert.Equal(t, scfg.Config["DD_KEY_4"], "value with \" quotes")
		assert.Equal(t, scfg.Config["DD_KEY_5"], "value with \\ backslash")
	})
}

func TestParseFile(t *testing.T) {
	t.Run("file doesn't exist", func(t *testing.T) {
		scfg := ParseFile("test.yml")
		assert.True(t, scfg.isEmpty())
	})
	t.Run("success", func(t *testing.T) {
		err := os.WriteFile("test.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test.yml")
		scfg := ParseFile("test.yml")
		assert.Equal(t, scfg.Id, 67890)
		assert.Equal(t, len(scfg.Config), 2)
		assert.Equal(t, scfg.Config["DD_KEY_1"], "value_1")
		assert.Equal(t, scfg.Config["DD_KEY_2"], "value_2")
	})
	t.Run("file with no read permissions", func(t *testing.T) {
		err := os.WriteFile("test.yml", []byte(validYaml), 0000) // No permissions
		assert.NoError(t, err)
		defer os.Remove("test.yml")
		scfg := ParseFile("test.yml")
		assert.True(t, scfg.isEmpty())
	})
}

func TestFileSizeLimits(t *testing.T) {
	t.Run("under limit", func(t *testing.T) {
		data := `
"config_id": 67890
"apm_configuration_default":
    "DD_APM_TRACING_ENABLED": "false"
    "DD_RUNTIME_METRICS_ENABLED": "false"
    "DD_LOGS_INJECTION": "false"
    "DD_PROFILING_ENABLED": "false"
    "DD_DATA_STREAMS_ENABLED": "false"
    "DD_APPSEC_ENABLED": "false"
    "DD_IAST_ENABLED": "false"
    "DD_DYNAMIC_INSTRUMENTATION_ENABLED": "false"
    "DD_DATA_JOBS_ENABLED": "false"
    "DD_APPSEC_SCA_ENABLED": "false"
    "DD_TRACE_DEBUG": "false"
`
		err := os.WriteFile("test.yml", []byte(data), 0644)
		assert.NoError(t, err)
		defer os.Remove("test.yml")
		scfg := ParseFile("test.yml")
		assert.False(t, scfg.isEmpty()) // file parsing succeeded
	})
	t.Run("over limit", func(t *testing.T) {
		// Build a valid stable configuration file that surpasses maxFileSize
		header := `"config_id": 67890
		"apm_configuration_default":
		`
		entry := `    "DD_TRACE_DEBUG": "false"`
		content := header
		for len(content) <= maxFileSize {
			content += entry
		}

		err := os.WriteFile("test.yml", []byte(content), 0644)
		assert.NoError(t, err)
		defer os.Remove("test.yml")
		scfg := ParseFile("test.yml")
		assert.True(t, scfg.isEmpty()) // file parsing succeeded
	})
}
