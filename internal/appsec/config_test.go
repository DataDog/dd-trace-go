// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	expectedDefaultConfig := &config{
		rules:      []byte(staticRecommendedRule),
		wafTimeout: defaultWAFTimeout,
	}

	t.Run("default", func(t *testing.T) {
		restoreEnv := cleanEnv()
		defer restoreEnv()
		cfg, err := newConfig()
		require.NoError(t, err)
		require.Equal(t, expectedDefaultConfig, cfg)
	})

	t.Run("waf-timeout", func(t *testing.T) {
		t.Run("parsable", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			require.NoError(t, os.Setenv(wafTimeoutEnvVar, "5s"))
			cfg, err := newConfig()
			require.NoError(t, err)
			require.Equal(
				t,
				&config{
					rules:      []byte(staticRecommendedRule),
					wafTimeout: 5 * time.Second,
				},
				cfg,
			)
		})

		t.Run("not-parsable", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			require.NoError(t, os.Setenv(wafTimeoutEnvVar, "not a duration string"))
			cfg, err := newConfig()
			require.NoError(t, err)
			require.Equal(t, expectedDefaultConfig, cfg)
		})

		t.Run("negative", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			require.NoError(t, os.Setenv(wafTimeoutEnvVar, "not a duration string"))
			cfg, err := newConfig()
			require.NoError(t, err)
			require.Equal(t, expectedDefaultConfig, cfg)
		})

		t.Run("empty-string", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			require.NoError(t, os.Setenv(wafTimeoutEnvVar, ""))
			cfg, err := newConfig()
			require.NoError(t, err)
			require.Equal(t, expectedDefaultConfig, cfg)
		})
	})

	t.Run("rules", func(t *testing.T) {
		t.Run("empty-string", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			os.Setenv(rulesEnvVar, "")
			cfg, err := newConfig()
			require.NoError(t, err)
			require.Equal(t, expectedDefaultConfig, cfg)
		})

		t.Run("file-not-found", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			os.Setenv(rulesEnvVar, "i do not exist")
			cfg, err := newConfig()
			require.Error(t, err)
			require.Nil(t, cfg)
		})

		t.Run("local-file", func(t *testing.T) {
			restoreEnv := cleanEnv()
			defer restoreEnv()
			file, err := ioutil.TempFile("", "example-*")
			require.NoError(t, err)
			defer func() {
				file.Close()
				os.Remove(file.Name())
			}()
			expectedRules := `custom rule file content`
			_, err = file.WriteString(expectedRules)
			require.NoError(t, err)
			os.Setenv(rulesEnvVar, file.Name())
			cfg, err := newConfig()
			require.NoError(t, err)
			require.Equal(t, &config{
				rules:      []byte(expectedRules),
				wafTimeout: defaultWAFTimeout,
			}, cfg)
		})
	})
}

func cleanEnv() func() {
	wafTimeout := os.Getenv(wafTimeoutEnvVar)
	if err := os.Unsetenv(wafTimeoutEnvVar); err != nil {
		panic(err)
	}
	rules := os.Getenv(rulesEnvVar)
	if err := os.Unsetenv(rulesEnvVar); err != nil {
		panic(err)
	}
	return func() {
		restoreEnv(wafTimeoutEnvVar, wafTimeout)
		restoreEnv(rulesEnvVar, rules)
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
