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

	waf "github.com/DataDog/go-libddwaf/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnabled(t *testing.T) {
	enabledConfig, _ := strconv.ParseBool(os.Getenv(config.EnvEnabled))
	wafSupported, _ := waf.Health()
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

func TestWafInitMetric(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/fp.json")
	telemetryClient := new(telemetrytest.RecordClient)
	telemetry.MockClient(telemetryClient)
	appsec.Start()
	defer appsec.Stop()
	if !appsec.Enabled() {
		t.Skip("AppSec is disabled")
	}

	assert.Equal(t, 1.0, telemetryClient.Count(telemetry.NamespaceAppSec, "waf.init", []string{
		"success:true",
		"waf_version:" + waf.Version(),
		"event_rules_version:1.4.2",
	}).Get())
}
