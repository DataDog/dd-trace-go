// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
	"github.com/DataDog/go-libddwaf/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnabled(t *testing.T) {
	enabledConfig, _ := strconv.ParseBool(os.Getenv(config.EnvEnabled))
	wafSupported, _ := libddwaf.Usable()
	canBeEnabled := enabledConfig && wafSupported

	require.False(t, appsec.Enabled())
	tracer.Start()
	assert.Equal(t, canBeEnabled, appsec.Enabled())
	tracer.Stop()
	assert.False(t, appsec.Enabled())
}

// Test that everything goes well when simply starting and stopping appsec
func TestStartStop(t *testing.T) {
	// Use t.Setenv() to automatically restore the initial env var value, if set
	t.Setenv(config.EnvEnabled, "")
	os.Unsetenv(config.EnvEnabled)
	testutils.StartAppSec(t)
	appsec.Stop()
}

func TestAppsecEnabledTelemetry(t *testing.T) {
	var telemetryClient telemetrytest.RecordClient
	defer telemetry.MockClient(&telemetryClient)()

	t.Run("default", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "")

		appsec.Start()
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: false, Origin: telemetry.OriginDefault})
	})

	t.Run("env_enabled", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "true")

		appsec.Start()
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: true, Origin: telemetry.OriginEnvVar})
	})

	t.Run("env_disable", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "false")

		appsec.Start()
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: false, Origin: telemetry.OriginEnvVar})
	})

	t.Run("code_enabled", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "")

		appsec.Start(config.WithEnablementMode(config.ForcedOn))
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: true, Origin: telemetry.OriginCode})
	})

	t.Run("code_enabled", func(t *testing.T) {
		t.Setenv(config.EnvEnabled, "")

		appsec.Start(config.WithEnablementMode(config.ForcedOff))
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: false, Origin: telemetry.OriginCode})
	})
}
