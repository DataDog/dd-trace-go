// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		t.Run("client-ip-header-unset", func(t *testing.T) {
			cfg := newConfig()
			require.Empty(t, cfg.ipHeader)

		})
		t.Run("client-ip-header-empty", func(t *testing.T) {
			restore := cleanEnv()
			err := os.Setenv(clientIPHeaderEnvVar, "")
			require.NoError(t, err)
			cfg := newConfig()
			require.Empty(t, cfg.ipHeader)
			defer restore()

		})
		t.Run("client-ip-header-set", func(t *testing.T) {
			restore := cleanEnv()
			err := os.Setenv(clientIPHeaderEnvVar, "custom-header")
			require.NoError(t, err)
			cfg := newConfig()
			require.Equal(t, "custom-header", cfg.ipHeader)
			defer restore()

		})
	})
}

func cleanEnv() func() {
	val := os.Getenv(clientIPHeaderEnvVar)
	if err := os.Unsetenv(clientIPHeaderEnvVar); err != nil {
		panic(err)
	}
	return func() {
		restoreEnv(clientIPHeaderEnvVar, val)
	}
}

func restoreEnv(key, value string) {
	if value != "" {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	} else {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
}
