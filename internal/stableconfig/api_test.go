// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import (
	"fmt"
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/stretchr/testify/assert"
)

func TestBool(t *testing.T) {
	// Test typical operation with valid files
	t.Run("valid configurations", func(t *testing.T) {
		tests := []struct {
			name           string
			localYaml      string           // YAML content for local config file
			managedYaml    string           // YAML content for managed config file
			envValue       string           // Environment variable value
			key            string           // Configuration key to test
			defaultValue   bool             // Default value to use
			expectedValue  bool             // Expected result value
			expectedOrigin telemetry.Origin // Expected origin of the value
			expectedErr    error            // Expected error, if any
		}{
			// When no config exists, return default value
			{
				name:           "default value",
				key:            "UNKNOWN_KEY",
				defaultValue:   true,
				expectedValue:  true,
				expectedOrigin: telemetry.OriginDefault,
			},
			//  Local config overrides default
			{
				name:           "local config only",
				localYaml:      "apm_configuration_default:\n    DD_KEY: true",
				key:            "DD_KEY",
				defaultValue:   false,
				expectedValue:  true,
				expectedOrigin: telemetry.OriginLocalStableConfig,
			},
			// Env var overrides local config
			{
				name:           "env overrides local",
				localYaml:      "apm_configuration_default:\n    DD_KEY: true",
				envValue:       "false",
				key:            "DD_KEY",
				defaultValue:   true,
				expectedValue:  false,
				expectedOrigin: telemetry.OriginEnvVar,
			},
			// Managed config overrides env var
			{
				name:           "managed overrides env",
				localYaml:      "apm_configuration_default:\n    DD_KEY: true",
				managedYaml:    "apm_configuration_default:\n    DD_KEY: false",
				envValue:       "true",
				key:            "DD_KEY",
				defaultValue:   true,
				expectedValue:  false,
				expectedOrigin: telemetry.OriginManagedStableConfig,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Setup test environment
				if tt.localYaml != "" {
					tempLocalPath := "local.yml"
					err := os.WriteFile(tempLocalPath, []byte(tt.localYaml), 0644)
					assert.NoError(t, err)
					defer os.Remove(tempLocalPath)
					LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
					defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
				}

				if tt.managedYaml != "" {
					tempManagedPath := "managed.yml"
					err := os.WriteFile(tempManagedPath, []byte(tt.managedYaml), 0644)
					assert.NoError(t, err)
					defer os.Remove(tempManagedPath)
					ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
					defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()
				}

				if tt.envValue != "" {
					t.Setenv(tt.key, tt.envValue)
				}

				val, origin, err := Bool(tt.key, tt.defaultValue)
				assert.Equal(t, tt.expectedValue, val)
				assert.Equal(t, tt.expectedOrigin, origin)
				assert.Equal(t, tt.expectedErr, err)
			})
		}
	})

	// Test error handling with invalid configurations
	t.Run("error handling", func(t *testing.T) {
		tests := []struct {
			name           string
			localYaml      string
			managedYaml    string
			envValue       string
			key            string
			defaultValue   bool
			expectedValue  bool
			expectedOrigin telemetry.Origin
			expectedErr    string
		}{
			// Invalid boolean in managed config
			{
				name:           "invalid managed config value",
				managedYaml:    "apm_configuration_default:\n    DD_KEY: not-a-bool",
				key:            "DD_KEY",
				defaultValue:   true,
				expectedValue:  true,
				expectedOrigin: telemetry.OriginDefault,
				expectedErr:    fmt.Sprintf("invalid value for DD_KEY: 'not-a-bool' in %s configuration, dropping", originManagedStableConfig),
			},
			// Invalid boolean in environment variable
			{
				name:           "invalid env value",
				envValue:       "not-a-bool",
				key:            "DD_KEY",
				defaultValue:   true,
				expectedValue:  true,
				expectedOrigin: telemetry.OriginDefault,
				expectedErr:    fmt.Sprintf("invalid value for DD_KEY: 'not-a-bool' in %s configuration, dropping", originEnvVar),
			},
			// Invalid boolean in local config
			{
				name:           "invalid local config value",
				localYaml:      "apm_configuration_default:\n    DD_KEY: not-a-bool",
				key:            "DD_KEY",
				defaultValue:   true,
				expectedValue:  true,
				expectedOrigin: telemetry.OriginDefault,
				expectedErr:    fmt.Sprintf("invalid value for DD_KEY: 'not-a-bool' in %s configuration, dropping", originLocalStableConfig),
			},
			// Empty string in config; no error expected
			{
				name:           "empty string in config",
				localYaml:      "apm_configuration_default:\n    DD_KEY: ''",
				key:            "DD_KEY",
				defaultValue:   true,
				expectedValue:  true,
				expectedOrigin: telemetry.OriginDefault,
				expectedErr:    "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Setup test environment
				if tt.localYaml != "" {
					tempLocalPath := "local.yml"
					err := os.WriteFile(tempLocalPath, []byte(tt.localYaml), 0644)
					assert.NoError(t, err)
					defer os.Remove(tempLocalPath)
					LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
					defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
				}

				if tt.managedYaml != "" {
					tempManagedPath := "managed.yml"
					err := os.WriteFile(tempManagedPath, []byte(tt.managedYaml), 0644)
					assert.NoError(t, err)
					defer os.Remove(tempManagedPath)
					ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
					defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()
				}

				if tt.envValue != "" {
					t.Setenv(tt.key, tt.envValue)
				}

				val, origin, err := Bool(tt.key, tt.defaultValue)
				assert.Equal(t, tt.expectedValue, val)
				assert.Equal(t, tt.expectedOrigin, origin)
				if tt.expectedErr != "" {
					assert.ErrorContains(t, err, tt.expectedErr)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}

func TestString(t *testing.T) {
	// Yaml content for local and managed files
	localYaml := `
apm_configuration_default:
    DD_KEY: local
`
	managedYaml := `
apm_configuration_default:
    DD_KEY: managed
`
	// Modify file paths for testing
	tempLocalPath := "local.yml"
	tempManagedPath := "managed.yml"
	// Write to local file, and defer delete it
	err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(tempLocalPath)
	// Write to managed file, and defer delete it
	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(tempManagedPath)

	t.Run("default", func(t *testing.T) {
		val, origin := String("UNKNOWN_KEY", "default")
		assert.Equal(t, "default", val)
		assert.Equal(t, telemetry.OriginDefault, origin)
	})
	t.Run("localStableconfig only", func(t *testing.T) {
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin := String("DD_KEY", "default")
		assert.Equal(t, "local", val)
		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
	})
	t.Run("env overrides localStableConfig", func(t *testing.T) {
		t.Setenv("DD_KEY", "env")
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin := String("DD_KEY", "default")
		assert.Equal(t, "env", val)
		assert.Equal(t, telemetry.OriginEnvVar, origin)
	})
	t.Run("managedStableConfig overrides env", func(t *testing.T) {
		t.Setenv("DD_KEY", "env")

		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()

		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()

		val, origin := String("DD_KEY", "default")
		assert.Equal(t, "managed", val)
		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
	})
}

func TestInt(t *testing.T) {
	// Yaml content for local and managed files
	localYaml := `apm_configuration_default:
    DD_KEY: 123`
	managedYaml := `apm_configuration_default:
    DD_KEY: 456`
	// Modify file paths for testing
	tempLocalPath := "local.yml"
	tempManagedPath := "managed.yml"
	// Write to local file, and defer delete it
	err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(tempLocalPath)
	// Write to managed file, and defer delete it
	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(tempManagedPath)

	t.Run("default", func(t *testing.T) {
		val, origin := Int("UNKNOWN_KEY", 1)
		assert.Equal(t, 1, val)
		assert.Equal(t, telemetry.OriginDefault, origin)
	})
	t.Run("localStableconfig only", func(t *testing.T) {
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin := Int("DD_KEY", 1)
		assert.Equal(t, 123, val)
		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
	})
	t.Run("env overrides localStableConfig", func(t *testing.T) {
		t.Setenv("DD_KEY", "2")
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin := Int("DD_KEY", 1)
		assert.Equal(t, 2, val)
		assert.Equal(t, telemetry.OriginEnvVar, origin)
	})
	t.Run("managedStableConfig overrides env", func(t *testing.T) {
		t.Setenv("DD_KEY", "2")

		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()

		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()

		val, origin := Int("DD_KEY", 1)
		assert.Equal(t, 456, val)
		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
	})
}

// func TestFloat(t *testing.T) {
// 	// Yaml content for local and managed files
// 	localYaml := `apm_configuration_default:
//     DD_KEY: 123.45`
// 	managedYaml := `apm_configuration_default:
//     DD_KEY: 456.78`
// 	// Modify file paths for testing
// 	tempLocalPath := "local.yml"
// 	tempManagedPath := "managed.yml"
// 	// Write to local file, and defer delete it
// 	err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
// 	assert.NoError(t, err)
// 	defer os.Remove(tempLocalPath)
// 	// Write to managed file, and defer delete it
// 	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
// 	assert.NoError(t, err)
// 	defer os.Remove(tempManagedPath)

// 	t.Run("default", func(t *testing.T) {
// 		val, origin := Float("UNKNOWN_KEY", 1.0)
// 		assert.Equal(t, 1.0, val)
// 		assert.Equal(t, telemetry.OriginDefault, origin)
// 	})
// 	t.Run("localStableconfig only", func(t *testing.T) {
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		val, origin := Float("DD_KEY", 1.0)
// 		assert.Equal(t, 123.45, val)
// 		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
// 	})
// 	t.Run("env overrides localStableConfig", func(t *testing.T) {
// 		t.Setenv("DD_KEY", "2.5")
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		val, origin := Float("DD_KEY", 1.0)
// 		assert.Equal(t, 2.5, val)
// 		assert.Equal(t, telemetry.OriginEnvVar, origin)
// 	})
// 	t.Run("managedStableConfig overrides env", func(t *testing.T) {
// 		t.Setenv("DD_KEY", "2.5")
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
// 		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()
// 		val, origin := Float("DD_KEY", 1.0)
// 		assert.Equal(t, 456.78, val)
// 		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
// 	})
// }

// func TestDuration(t *testing.T) {
// 	// Yaml content for local and managed files
// 	localYaml := `apm_configuration_default:
//     DD_KEY: 1h`
// 	managedYaml := `apm_configuration_default:
//     DD_KEY: 2h`
// 	// Modify file paths for testing
// 	tempLocalPath := "local.yml"
// 	tempManagedPath := "managed.yml"
// 	// Write to local file, and defer delete it
// 	err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
// 	assert.NoError(t, err)
// 	defer os.Remove(tempLocalPath)
// 	// Write to managed file, and defer delete it
// 	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
// 	assert.NoError(t, err)
// 	defer os.Remove(tempManagedPath)

// 	t.Run("default", func(t *testing.T) {
// 		val, origin := Duration("UNKNOWN_KEY", time.Hour)
// 		assert.Equal(t, time.Hour, val)
// 		assert.Equal(t, telemetry.OriginDefault, origin)
// 	})
// 	t.Run("localStableconfig only", func(t *testing.T) {
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		val, origin := Duration("DD_KEY", time.Hour)
// 		assert.Equal(t, time.Hour, val)
// 		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
// 	})
// 	t.Run("env overrides localStableConfig", func(t *testing.T) {
// 		t.Setenv("DD_KEY", "30m")
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		val, origin := Duration("DD_KEY", time.Hour)
// 		assert.Equal(t, 30*time.Minute, val)
// 		assert.Equal(t, telemetry.OriginEnvVar, origin)
// 	})
// 	t.Run("managedStableConfig overrides env", func(t *testing.T) {
// 		t.Setenv("DD_KEY", "30m")
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
// 		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()
// 		val, origin := Duration("DD_KEY", time.Hour)
// 		assert.Equal(t, 2*time.Hour, val)
// 		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
// 	})
// }

// func TestIP(t *testing.T) {
// 	// Yaml content for local and managed files
// 	localYaml := `apm_configuration_default:
//     DD_KEY: 127.0.0.1`
// 	managedYaml := `apm_configuration_default:
//     DD_KEY: 192.168.1.1`
// 	// Modify file paths for testing
// 	tempLocalPath := "local.yml"
// 	tempManagedPath := "managed.yml"
// 	// Write to local file, and defer delete it
// 	err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
// 	assert.NoError(t, err)
// 	defer os.Remove(tempLocalPath)
// 	// Write to managed file, and defer delete it
// 	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
// 	assert.NoError(t, err)
// 	defer os.Remove(tempManagedPath)

// 	defaultIP := net.ParseIP("0.0.0.0")
// 	localIP := net.ParseIP("127.0.0.1")
// 	envIP := net.ParseIP("10.0.0.1")
// 	managedIP := net.ParseIP("192.168.1.1")

// 	t.Run("default", func(t *testing.T) {
// 		val, origin := IP("UNKNOWN_KEY", defaultIP)
// 		assert.Equal(t, defaultIP, val)
// 		assert.Equal(t, telemetry.OriginDefault, origin)
// 	})
// 	t.Run("localStableconfig only", func(t *testing.T) {
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		val, origin := IP("DD_KEY", defaultIP)
// 		assert.Equal(t, localIP, val)
// 		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
// 	})
// 	t.Run("env overrides localStableConfig", func(t *testing.T) {
// 		t.Setenv("DD_KEY", "10.0.0.1")
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		val, origin := IP("DD_KEY", defaultIP)
// 		assert.Equal(t, envIP, val)
// 		assert.Equal(t, telemetry.OriginEnvVar, origin)
// 	})
// 	t.Run("managedStableConfig overrides env", func(t *testing.T) {
// 		t.Setenv("DD_KEY", "10.0.0.1")
// 		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
// 		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
// 		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
// 		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()
// 		val, origin := IP("DD_KEY", defaultIP)
// 		assert.Equal(t, managedIP, val)
// 		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
// 	})
// }
