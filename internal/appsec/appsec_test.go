// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/go-libddwaf/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
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
	t.Run("default", func(t *testing.T) {
		var telemetryClient telemetrytest.RecordClient
		defer telemetry.MockClient(&telemetryClient)()
		t.Setenv(config.EnvEnabled, "")

		appsec.Start()
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: false, Origin: telemetry.OriginDefault})
	})

	t.Run("env_enabled", func(t *testing.T) {
		var telemetryClient telemetrytest.RecordClient
		defer telemetry.MockClient(&telemetryClient)()
		t.Setenv(config.EnvEnabled, "true")

		appsec.Start()
		defer appsec.Stop()

		shouldBeEnabled, _ := libddwaf.Usable()
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: shouldBeEnabled, Origin: telemetry.OriginEnvVar})
	})

	t.Run("env_disable", func(t *testing.T) {
		var telemetryClient telemetrytest.RecordClient
		defer telemetry.MockClient(&telemetryClient)()
		t.Setenv(config.EnvEnabled, "false")

		appsec.Start()
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: false, Origin: telemetry.OriginEnvVar})
	})

	t.Run("code_enabled", func(t *testing.T) {
		var telemetryClient telemetrytest.RecordClient
		defer telemetry.MockClient(&telemetryClient)()
		t.Setenv(config.EnvEnabled, "")

		appsec.Start(config.WithEnablementMode(config.ForcedOn))
		defer appsec.Stop()

		shouldBeEnabled, _ := libddwaf.Usable()
		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: shouldBeEnabled, Origin: telemetry.OriginCode})
	})

	t.Run("code_enabled", func(t *testing.T) {
		var telemetryClient telemetrytest.RecordClient
		defer telemetry.MockClient(&telemetryClient)()
		t.Setenv(config.EnvEnabled, "")

		appsec.Start(config.WithEnablementMode(config.ForcedOff))
		defer appsec.Stop()

		assert.Contains(t, telemetryClient.Configuration, telemetry.Configuration{Name: config.EnvEnabled, Value: false, Origin: telemetry.OriginCode})
	})
}

// TestNoAppsecErrorWhenRCDisabled reproduces the second issue from #4404:
// When DD_REMOTE_CONFIGURATION_ENABLED=false and DD_APPSEC_ENABLED is not set,
// AppSec should not log an ERROR about "remote config client not started".
// AppSec is in RCStandby mode (waiting to be activated via RC), but since RC
// is explicitly disabled, it simply can't be remotely activated — which is
// expected and should not be treated as an unexpected error.
func TestNoAppsecErrorWhenRCDisabled(t *testing.T) {
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	// Ensure DD_APPSEC_ENABLED is not set (RCStandby mode)
	t.Setenv(config.EnvEnabled, "")
	os.Unsetenv(config.EnvEnabled)

	var logger log.RecordLogger
	defer log.UseLogger(&logger)()

	// Provide a non-nil RC config, as the tracer always does
	rcCfg := remoteconfig.DefaultClientConfig()
	appsec.Start(config.WithRCConfig(rcCfg))
	defer appsec.Stop()

	// Flush the error log aggregator to force any pending errors to be recorded.
	log.Flush()

	for _, entry := range logger.Logs() {
		if strings.Contains(entry, "ERROR") {
			t.Errorf("unexpected ERROR log when RC is disabled: %s", entry)
		}
	}
}
