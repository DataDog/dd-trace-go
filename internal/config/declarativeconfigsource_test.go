// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"bytes"
	"os"
	"runtime"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
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

// testLogger implements a mock Logger that captures output
type testLogger struct {
	buf bytes.Buffer
}

func (l *testLogger) Log(msg string) {
	l.buf.WriteString(msg)
}

func (l *testLogger) String() string {
	return l.buf.String()
}

func (l *testLogger) Reset() {
	l.buf.Reset()
}

// Helper function to check if declarativeConfig is empty
func isEmptyDeclarativeConfig(dc *declarativeConfig) bool {
	return dc.ID == telemetry.EmptyID && len(dc.Config) == 0
}

func TestFileContentsToConfig(t *testing.T) {
	t.Run("simple failure", func(t *testing.T) {
		data := `
a: Easy!
b:
  c: 2
  d: [3, 4]
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
	})

	t.Run("simple success", func(t *testing.T) {
		dc := fileContentsToConfig([]byte(validYaml), "test.yml")
		assert.Equal(t, "67890", dc.ID)
		assert.Equal(t, 2, len(dc.Config))
		assert.Equal(t, "value_1", dc.Config["DD_KEY_1"])
		assert.Equal(t, "value_2", dc.Config["DD_KEY_2"])
	})

	t.Run("success without apm_configuration_default", func(t *testing.T) {
		data := `
config_id: 67890
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, 0, len(dc.Config))
		assert.Equal(t, "67890", dc.ID)
	})

	t.Run("success without config_id", func(t *testing.T) {
		data := `
apm_configuration_default:
    DD_KEY_1: value_1
    "DD_KEY_2": "value_2"
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, 2, len(dc.Config))
		assert.Equal(t, "value_1", dc.Config["DD_KEY_1"])
		assert.Equal(t, "value_2", dc.Config["DD_KEY_2"])
		assert.Equal(t, "", dc.ID)
	})

	t.Run("success with empty contents", func(t *testing.T) {
		dc := fileContentsToConfig([]byte(``), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
	})

	t.Run("numeric values", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: 123
    DD_KEY_2: 3.14
    DD_KEY_3: -42
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, "67890", dc.ID)
		assert.Equal(t, 3, len(dc.Config))
		assert.Equal(t, "123", dc.Config["DD_KEY_1"])
		assert.Equal(t, "3.14", dc.Config["DD_KEY_2"])
		assert.Equal(t, "-42", dc.Config["DD_KEY_3"])
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
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, "67890", dc.ID)
		assert.Equal(t, 4, len(dc.Config))
		assert.Equal(t, "true", dc.Config["DD_KEY_1"])
		assert.Equal(t, "false", dc.Config["DD_KEY_2"])
		assert.Equal(t, "yes", dc.Config["DD_KEY_3"])
		assert.Equal(t, "no", dc.Config["DD_KEY_4"])
	})

	t.Run("malformed YAML - missing colon", func(t *testing.T) {
		data := `
config_id 67890
apm_configuration_default
    DD_KEY_1 value_1
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
	})

	t.Run("malformed YAML - incorrect indentation", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
DD_KEY_1: value_1
  DD_KEY_2: value_2
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
	})

	t.Run("malformed YAML - duplicate keys", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: value_1
    DD_KEY_1: value_2
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc)) // yaml.v3 treats duplicate keys as an error
	})

	t.Run("malformed YAML - unclosed quotes", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default:
    DD_KEY_1: "value_1
    DD_KEY_2: value_2
`
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
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
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
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
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.Equal(t, "67890", dc.ID)
		assert.Equal(t, 5, len(dc.Config))
		assert.Equal(t, "value with spaces", dc.Config["DD_KEY_1"])
		assert.Equal(t, "value with \n newline", dc.Config["DD_KEY_2"])
		assert.Equal(t, "value with \t tab", dc.Config["DD_KEY_3"])
		assert.Equal(t, "value with \" quotes", dc.Config["DD_KEY_4"])
		assert.Equal(t, "value with \\ backslash", dc.Config["DD_KEY_5"])
	})
}

func TestParseFile(t *testing.T) {
	t.Run("file doesn't exist", func(t *testing.T) {
		dc := parseFile("test_nonexistent.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
	})

	t.Run("success", func(t *testing.T) {
		err := os.WriteFile("test_declarative.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_declarative.yml")

		dc := parseFile("test_declarative.yml")
		assert.Equal(t, "67890", dc.ID)
		assert.Equal(t, 2, len(dc.Config))
		assert.Equal(t, "value_1", dc.Config["DD_KEY_1"])
		assert.Equal(t, "value_2", dc.Config["DD_KEY_2"])
	})

	t.Run("file with no read permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("File permission restrictions don't work reliably on Windows - the OS often grants read access to file owners regardless of permission bits")
		}

		// On Unix-like systems, create file with no read permissions
		err := os.WriteFile("test_declarative_noperm.yml", []byte(validYaml), 0000)
		assert.NoError(t, err)
		defer os.Remove("test_declarative_noperm.yml")

		dc := parseFile("test_declarative_noperm.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
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
		err := os.WriteFile("test_declarative_small.yml", []byte(data), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_declarative_small.yml")

		dc := parseFile("test_declarative_small.yml")
		assert.False(t, isEmptyDeclarativeConfig(dc)) // file parsing succeeded
	})

	t.Run("over limit", func(t *testing.T) {
		// Build a valid declarative configuration file that surpasses maxFileSize
		header := `"config_id": 67890
		"apm_configuration_default":
		`
		entry := `    "DD_TRACE_DEBUG": "false"`
		content := header
		for len(content) <= maxFileSize {
			content += entry
		}

		err := os.WriteFile("test_declarative_large.yml", []byte(content), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_declarative_large.yml")

		dc := parseFile("test_declarative_large.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc)) // file parsing failed due to size
	})
}

func TestParseFileLogging(t *testing.T) {
	// Capture log output
	tl := &testLogger{}
	defer log.UseLogger(tl)()

	t.Run("non-existent file on non-Linux doesn't log on stat", func(t *testing.T) {
		if runtime.GOOS == "linux" {
			t.Skip("This test is for non-linux platforms")
		}

		tl.Reset()
		dc := parseFile("test_nonexistent_declarative.yml")
		assert.NotNil(t, dc)
		assert.Empty(t, dc.Config)
		// Should not log warnings for non-existent files
		assert.NotContains(t, tl.String(), "Failed to stat")
	})

	t.Run("directory instead of file logs warning", func(t *testing.T) {
		dirPath := "test_declarative_dir"
		err := os.MkdirAll(dirPath, 0755)
		assert.NoError(t, err)
		defer os.RemoveAll(dirPath)

		tl.Reset()
		dc := parseFile(dirPath)
		assert.NotNil(t, dc)
		assert.Empty(t, dc.Config)
		assert.Contains(t, tl.String(), "Failed to read declarative config file")
	})

	t.Run("malformed YAML logs warning", func(t *testing.T) {
		data := `
config_id: 67890
apm_configuration_default
    DD_KEY_1 value_1
`
		tl.Reset()
		dc := fileContentsToConfig([]byte(data), "test.yml")
		assert.True(t, isEmptyDeclarativeConfig(dc))
		assert.Contains(t, tl.String(), "Parsing declarative config file test.yml failed")
	})
}

func TestDeclarativeConfigSource(t *testing.T) {
	t.Run("Get returns value from config", func(t *testing.T) {
		err := os.WriteFile("test_source.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_source.yml")

		source := newDeclarativeConfigSource("test_source.yml", telemetry.OriginLocalStableConfig)
		assert.Equal(t, "value_1", source.Get("DD_KEY_1"))
		assert.Equal(t, "value_2", source.Get("DD_KEY_2"))
	})

	t.Run("Get normalizes key", func(t *testing.T) {
		err := os.WriteFile("test_source_normalize.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_source_normalize.yml")

		source := newDeclarativeConfigSource("test_source_normalize.yml", telemetry.OriginLocalStableConfig)
		// Should normalize "key_1" to "DD_KEY_1"
		assert.Equal(t, "value_1", source.Get("key_1"))
	})

	t.Run("Get returns empty for missing key", func(t *testing.T) {
		err := os.WriteFile("test_source_missing.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_source_missing.yml")

		source := newDeclarativeConfigSource("test_source_missing.yml", telemetry.OriginLocalStableConfig)
		assert.Equal(t, "", source.Get("DD_NONEXISTENT_KEY"))
	})

	t.Run("GetID returns config ID", func(t *testing.T) {
		err := os.WriteFile("test_source_id.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_source_id.yml")

		source := newDeclarativeConfigSource("test_source_id.yml", telemetry.OriginLocalStableConfig)
		assert.Equal(t, "67890", source.GetID())
	})

	t.Run("GetID returns empty for missing ID", func(t *testing.T) {
		data := `
apm_configuration_default:
    DD_KEY_1: value_1
`
		err := os.WriteFile("test_source_noid.yml", []byte(data), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_source_noid.yml")

		source := newDeclarativeConfigSource("test_source_noid.yml", telemetry.OriginLocalStableConfig)
		assert.Equal(t, "", source.GetID())
	})

	t.Run("Origin returns correct origin", func(t *testing.T) {
		err := os.WriteFile("test_source_origin.yml", []byte(validYaml), 0644)
		assert.NoError(t, err)
		defer os.Remove("test_source_origin.yml")

		localSource := newDeclarativeConfigSource("test_source_origin.yml", telemetry.OriginLocalStableConfig)
		assert.Equal(t, telemetry.OriginLocalStableConfig, localSource.Origin())

		managedSource := newDeclarativeConfigSource("test_source_origin.yml", telemetry.OriginManagedStableConfig)
		assert.Equal(t, telemetry.OriginManagedStableConfig, managedSource.Origin())
	})

	t.Run("non-existent file creates empty config", func(t *testing.T) {
		source := newDeclarativeConfigSource("nonexistent_config.yml", telemetry.OriginLocalStableConfig)
		assert.Equal(t, "", source.Get("DD_ANY_KEY"))
		assert.Equal(t, telemetry.EmptyID, source.GetID())
		assert.Equal(t, telemetry.OriginLocalStableConfig, source.Origin())
	})
}

func TestDeclarativeConfigConstants(t *testing.T) {
	t.Run("file paths are defined", func(t *testing.T) {
		assert.Equal(t, "/etc/datadog-agent/application_monitoring.yaml", localFilePath)
		assert.Equal(t, "/etc/datadog-agent/managed/datadog-agent/stable/application_monitoring.yaml", managedFilePath)
	})

	t.Run("max file size is 4KB", func(t *testing.T) {
		assert.Equal(t, 4*1024, maxFileSize)
	})
}

func TestGlobalDeclarativeConfigSources(t *testing.T) {
	t.Run("LocalDeclarativeConfig is initialized", func(t *testing.T) {
		assert.NotNil(t, LocalDeclarativeConfig)
		assert.Equal(t, telemetry.OriginLocalStableConfig, LocalDeclarativeConfig.Origin())
	})

	t.Run("ManagedDeclarativeConfig is initialized", func(t *testing.T) {
		assert.NotNil(t, ManagedDeclarativeConfig)
		assert.Equal(t, telemetry.OriginManagedStableConfig, ManagedDeclarativeConfig.Origin())
	})
}

