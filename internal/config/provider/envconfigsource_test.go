// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestEnvConfigSource(t *testing.T) {
	src := &envConfigSource{}
	t.Setenv("DD_SERVICE", "value")
	assert.Equal(t, "value", src.get("DD_SERVICE"))
	assert.Equal(t, telemetry.OriginEnvVar, src.origin())
}

func TestNormalizedEnvConfigSource(t *testing.T) {
	src := &envConfigSource{}
	t.Setenv("DD_SERVICE", "value")
	assert.Equal(t, "value", src.get("service"))
}
