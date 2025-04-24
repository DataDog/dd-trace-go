// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package stableconfig

import (
	"os"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/stretchr/testify/assert"
)

func TestBoolStableConfig(t *testing.T) {
	// Yaml content for local and managed files
	localYaml := `
apm_configuration_default:
    DD_KEY: true
`
	managedYaml := `
apm_configuration_default:
    DD_KEY: false
`
	// Modify file paths for testing
	tempLocalPath := "local.yml"
	tempManagedPath := "managed.yml"
	// Write to local file, and defer delete it
	err := os.WriteFile(tempLocalPath, []byte(localYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(localYaml)
	// Write to managed file, and defer delete it
	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(managedYaml)
	t.Run("default", func(t *testing.T) {
		val, origin, configured := BoolStableConfig("UNKNOWN_KEY", true)
		assert.True(t, val)
		assert.Equal(t, telemetry.OriginDefault, origin)
		assert.False(t, configured)
	})
	t.Run("localStableconfig only", func(t *testing.T) {
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin, configured := BoolStableConfig("DD_KEY", false)
		assert.True(t, val)
		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
		assert.True(t, configured)
	})
	t.Run("env overrides localStableConfig", func(t *testing.T) {
		t.Setenv("DD_KEY", "false")
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin, configured := BoolStableConfig("DD_KEY", true)
		assert.False(t, val)
		assert.Equal(t, telemetry.OriginEnvVar, origin)
		assert.True(t, configured)
	})
	t.Run("managedStableConfig overrides env", func(t *testing.T) {
		t.Setenv("DD_KEY", "true")

		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()

		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()

		val, origin, configured := BoolStableConfig("DD_KEY", true)
		assert.False(t, val)
		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
		assert.True(t, configured)
	})
}

func TestStringStableConfig(t *testing.T) {
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
	defer os.Remove(localYaml)
	// Write to managed file, and defer delete it
	err = os.WriteFile(tempManagedPath, []byte(managedYaml), 0644)
	assert.NoError(t, err)
	defer os.Remove(managedYaml)

	t.Run("default", func(t *testing.T) {
		val, origin := StringStableConfig("UNKNOWN_KEY", "default")
		assert.Equal(t, "default", val)
		assert.Equal(t, telemetry.OriginDefault, origin)
	})
	t.Run("localStableconfig only", func(t *testing.T) {
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin := StringStableConfig("DD_KEY", "default")
		assert.Equal(t, "local", val)
		assert.Equal(t, telemetry.OriginLocalStableConfig, origin)
	})
	t.Run("env overrides localStableConfig", func(t *testing.T) {
		t.Setenv("DD_KEY", "env")
		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()
		val, origin := StringStableConfig("DD_KEY", "default")
		assert.Equal(t, "env", val)
		assert.Equal(t, telemetry.OriginEnvVar, origin)
	})
	t.Run("managedStableConfig overrides env", func(t *testing.T) {
		t.Setenv("DD_KEY", "env")

		LocalConfig = newStableConfigSource(tempLocalPath, telemetry.OriginLocalStableConfig)
		defer func() { LocalConfig = newStableConfigSource(localFilePath, telemetry.OriginLocalStableConfig) }()

		ManagedConfig = newStableConfigSource(tempManagedPath, telemetry.OriginManagedStableConfig)
		defer func() { ManagedConfig = newStableConfigSource(managedFilePath, telemetry.OriginManagedStableConfig) }()

		val, origin := StringStableConfig("DD_KEY", "default")
		assert.Equal(t, "managed", val)
		assert.Equal(t, telemetry.OriginManagedStableConfig, origin)
	})
}
