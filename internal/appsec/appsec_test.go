// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package appsec_test

import (
	"os"
	"strconv"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/config"

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
	appsec.Start()
	appsec.Stop()
}
