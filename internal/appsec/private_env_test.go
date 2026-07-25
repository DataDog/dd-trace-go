// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package appsec

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/appsec/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func TestNewStartConfigPrivateBooleanGates(t *testing.T) {
	const (
		blockingKey = "_DD_APPSEC_BLOCKING_UNAVAILABLE"
		proxyKey    = "_DD_APPSEC_PROXY_ENVIRONMENT"
	)
	tests := []struct {
		name    string
		key     string
		raw     string
		present bool
		want    bool
		warn    bool
	}{
		{name: "blocking absent", key: blockingKey},
		{name: "blocking valid false", key: blockingKey, raw: "false", present: true},
		{name: "blocking valid true", key: blockingKey, raw: "1", present: true, want: true},
		{name: "blocking invalid", key: blockingKey, raw: "invalid", present: true, warn: true},
		{name: "blocking explicit empty", key: blockingKey, present: true, warn: true},
		{name: "proxy absent", key: proxyKey},
		{name: "proxy valid false", key: proxyKey, raw: "0", present: true},
		{name: "proxy valid true", key: proxyKey, raw: "TRUE", present: true, want: true},
		{name: "proxy invalid", key: proxyKey, raw: "invalid", present: true, warn: true},
		{name: "proxy explicit empty", key: proxyKey, present: true, warn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unsetPrivateAppSecEnv(t, blockingKey)
			unsetPrivateAppSecEnv(t, proxyKey)
			if tt.present {
				require.NoError(t, os.Setenv(tt.key, tt.raw))
			}
			logger := new(log.RecordLogger)
			defer log.UseLogger(logger)()

			startConfig := newStartConfig()
			switch tt.key {
			case blockingKey:
				require.Equal(t, tt.want, startConfig.BlockingUnavailable)
			case proxyKey:
				apiSecurity := new(config.APISecConfig)
				for _, option := range startConfig.APISecOptions {
					option(apiSecurity)
				}
				require.Equal(t, tt.want, apiSecurity.IsProxy)
			}
			logs := strings.Join(logger.Logs(), "\n")
			if tt.warn {
				require.Contains(t, logs, "Non-boolean value for env var "+tt.key+". Parse failed with error:")
			} else {
				require.Empty(t, logger.Logs())
			}
		})
	}
}

func TestNewStartConfigEnvironmentOptionPrecedence(t *testing.T) {
	const (
		blockingKey = "_DD_APPSEC_BLOCKING_UNAVAILABLE"
		proxyKey    = "_DD_APPSEC_PROXY_ENVIRONMENT"
	)
	t.Run("environment true overrides earlier blocking false", func(t *testing.T) {
		unsetPrivateAppSecEnv(t, blockingKey)
		unsetPrivateAppSecEnv(t, proxyKey)
		require.NoError(t, os.Setenv(blockingKey, "true"))
		require.True(t, newStartConfig(config.WithBlockingUnavailable(false)).BlockingUnavailable)
	})
	t.Run("environment false leaves earlier blocking true", func(t *testing.T) {
		unsetPrivateAppSecEnv(t, blockingKey)
		unsetPrivateAppSecEnv(t, proxyKey)
		require.NoError(t, os.Setenv(blockingKey, "false"))
		require.True(t, newStartConfig(config.WithBlockingUnavailable(true)).BlockingUnavailable)
	})
}

func unsetPrivateAppSecEnv(t *testing.T, key string) {
	t.Helper()
	old, present := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if present {
			require.NoError(t, os.Setenv(key, old))
		} else {
			require.NoError(t, os.Unsetenv(key))
		}
	})
}
