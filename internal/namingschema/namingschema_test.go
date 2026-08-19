// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package namingschema

import (
	"testing"

	"github.com/stretchr/testify/assert"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestReloadConfigRefreshesNamingSchemaWithoutReplacingGlobalConfig(t *testing.T) {
	t.Cleanup(func() {
		internalconfig.CreateNew()
		ReloadConfig()
	})
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "false")
	globalConfig := internalconfig.CreateNew()
	globalConfig.SetEnv("programmatic-env", internalconfig.OriginCode)

	assertSchema := func(want Version) {
		t.Helper()
		ReloadConfig()
		assert.Equal(t, want, GetVersion())
		assert.Same(t, globalConfig, internalconfig.Get())
		assert.Equal(t, "programmatic-env", internalconfig.Get().Env())
		assert.Equal(t, int(SchemaV0), internalconfig.Get().SpanAttributeSchemaVersion())
	}

	assertSchema(SchemaV0)

	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
	assertSchema(SchemaV1)

	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")
	assertSchema(SchemaV0)

	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "false")
	assertSchema(SchemaV1)
}

func TestNamingSchema(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		internalconfig.CreateNew()
		LoadFromEnv()

		cfg := GetConfig()
		assert.EqualValues(t, 0, cfg.NamingSchemaVersion)
		assert.Equal(t, false, cfg.RemoveIntegrationServiceNames)
		assert.Equal(t, "", cfg.DDService)
	})

	t.Run("env-vars", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
		t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

		internalconfig.CreateNew()
		LoadFromEnv()

		cfg := GetConfig()
		assert.EqualValues(t, 1, cfg.NamingSchemaVersion)
		assert.Equal(t, true, cfg.RemoveIntegrationServiceNames)
		assert.Equal(t, "", cfg.DDService)
	})

	t.Run("options", func(t *testing.T) {
		internalconfig.CreateNew()
		LoadFromEnv()
		SetRemoveIntegrationServiceNames(true)

		cfg := GetConfig()
		assert.EqualValues(t, 0, cfg.NamingSchemaVersion)
		assert.Equal(t, true, cfg.RemoveIntegrationServiceNames)
		assert.Equal(t, "", cfg.DDService)
	})

	t.Run("fallback to v0", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "invalid")
		t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

		internalconfig.CreateNew()
		LoadFromEnv()

		cfg := GetConfig()
		assert.EqualValues(t, 0, cfg.NamingSchemaVersion)
		assert.Equal(t, true, cfg.RemoveIntegrationServiceNames)
		assert.Equal(t, "", cfg.DDService)
	})

}
