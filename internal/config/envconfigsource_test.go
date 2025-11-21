// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func TestEnvConfigSource(t *testing.T) {
	envConfigSource := &envConfigSource{}
	t.Setenv("DD_SERVICE", "value")
	assert.Equal(t, "value", envConfigSource.Get("DD_SERVICE"))
	assert.Equal(t, telemetry.OriginEnvVar, envConfigSource.Origin())
}

func TestNormalizedEnvConfigSource(t *testing.T) {
	envConfigSource := &envConfigSource{}
	t.Setenv("DD_SERVICE", "value")
	assert.Equal(t, "value", envConfigSource.Get("service"))
}
