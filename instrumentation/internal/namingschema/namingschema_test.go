// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package namingschema

import (
	"testing"

	"github.com/stretchr/testify/assert"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestReloadConfigRefreshesEffectiveConfig(t *testing.T) {
	t.Cleanup(ReloadConfig)
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "false")

	assertSchema := func(want Version) {
		t.Helper()
		ReloadConfig()
		assert.Equal(t, want, GetVersion())
		assert.Equal(t, int(want), internalconfig.Get().SpanAttributeSchemaVersion())
	}

	assertSchema(VersionV0)

	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
	assertSchema(VersionV1)

	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")
	assertSchema(VersionV0)

	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "false")
	assertSchema(VersionV1)
}

func TestLoadFromEnvUsesEffectiveSpanAttributeSchema(t *testing.T) {
	t.Cleanup(func() {
		internalconfig.CreateNew()
		LoadFromEnv()
	})
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")
	internalconfig.CreateNew()

	LoadFromEnv()

	assert.Equal(t, VersionV0, GetVersion())
}
