// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOptions(t *testing.T) {
	t.Run("WithAPIKey", func(t *testing.T) {
		var cfg config
		WithAPIKey("123")(&cfg)
		assert.Equal(t, "123", cfg.apiKey)
	})

	t.Run("WithURL", func(t *testing.T) {
		var cfg config
		WithURL("my-url")(&cfg)
		assert.Equal(t, "my-url", cfg.apiURL)
	})

	t.Run("WithHostname", func(t *testing.T) {
		var cfg config
		WithHostname("my-hostname")(&cfg)
		assert.Equal(t, "my-hostname", cfg.hostname)
	})

	t.Run("WithPeriod", func(t *testing.T) {
		var cfg config
		WithPeriod(2 * time.Second)(&cfg)
		assert.Equal(t, 2*time.Second, cfg.period)
	})

	t.Run("CPUDuration", func(t *testing.T) {
		var cfg config
		CPUDuration(3 * time.Second)(&cfg)
		assert.Equal(t, 3*time.Second, cfg.cpuDuration)
	})

	t.Run("WithProfileTypes", func(t *testing.T) {
		var cfg config
		WithProfileTypes(HeapProfile)(&cfg)
		_, ok := cfg.types[HeapProfile]
		assert.True(t, ok)
		assert.Len(t, cfg.types, 1)
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		cfg := defaultConfig()
		assert := assert.New(t)
		assert.Equal(defaultAPIURL, cfg.apiURL)
		if v := os.Getenv("DD_ENV"); v != "" {
			assert.Equal(v, cfg.env)
		} else {
			assert.Equal(defaultEnv, cfg.env)
		}
		assert.Equal(filepath.Base(os.Args[0]), cfg.service)
		assert.Equal(len(defaultProfileTypes), len(cfg.types))
		for _, pt := range defaultProfileTypes {
			_, ok := cfg.types[pt]
			assert.True(ok)
		}
		_, ok := cfg.statsd.(noopStatsdClient)
		assert.True(ok)
		assert.Equal(DefaultPeriod, cfg.period)
		assert.Equal(DefaultDuration, cfg.cpuDuration)
		assert.Equal(DefaultMutexFraction, cfg.mutexFraction)
		assert.Equal(DefaultBlockRate, cfg.blockRate)
	})

	t.Run("env", func(t *testing.T) {
		env, val := "DD_API_KEY", "123"
		t.Run(env, func(t *testing.T) {
			os.Setenv(env, val)
			cfg := defaultConfig()
			assert.Equal(t, val, cfg.apiKey)
			os.Unsetenv(env)
		})

		env, val = "DD_HOSTNAME", "my-hostname"
		t.Run(env, func(t *testing.T) {
			os.Setenv(env, val)
			cfg := defaultConfig()
			assert.Equal(t, val, cfg.hostname)
			os.Unsetenv(env)
		})

		env, val = "DD_ENV", "my-env"
		t.Run(env, func(t *testing.T) {
			os.Setenv(env, val)
			cfg := defaultConfig()
			assert.Equal(t, val, cfg.env)
			os.Unsetenv(env)
		})

		env, val = "DD_SERVICE_NAME", "my-service"
		t.Run(env, func(t *testing.T) {
			os.Setenv(env, val)
			cfg := defaultConfig()
			assert.Equal(t, val, cfg.service)
			os.Unsetenv(env)
		})

		env, val = "DD_PROFILE_URL", "http://my.url"
		t.Run(env, func(t *testing.T) {
			os.Setenv(env, val)
			cfg := defaultConfig()
			assert.Equal(t, val, cfg.apiURL)
			os.Unsetenv(env)
		})

		env, val = "DD_PROFILE_TAGS", "a:b,c:d"
		t.Run(env, func(t *testing.T) {
			os.Setenv(env, val)
			cfg := defaultConfig()
			assert.ElementsMatch(t, []string{
				"a:b",
				"c:d",
				fmt.Sprintf("pid:%d", os.Getpid()),
			}, cfg.tags)
			os.Unsetenv(env)
		})
	})
}

func TestAddProfileType(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := defaultConfig()
		_, ok := cfg.types[MutexProfile]
		assert.False(t, ok)
		n := len(cfg.types)
		cfg.addProfileType(MutexProfile)
		assert.Len(t, cfg.types, n+1)
		_, ok = cfg.types[MutexProfile]
		assert.True(t, ok)
	})

	t.Run("nil", func(t *testing.T) {
		var cfg config
		assert.Nil(t, cfg.types)
		cfg.addProfileType(MutexProfile)
		assert.Len(t, cfg.types, 1)
		_, ok := cfg.types[MutexProfile]
		assert.True(t, ok)
	})
}
