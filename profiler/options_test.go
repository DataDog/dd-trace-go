// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/statsd"
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

	t.Run("WithService", func(t *testing.T) {
		var cfg config
		WithService("serviceName")(&cfg)
		assert.Equal(t, "serviceName", cfg.service)
	})

	t.Run("WithService/override", func(t *testing.T) {
		os.Setenv("DD_SERVICE", "envService")
		defer os.Unsetenv("DD_SERVICE")
		cfg := defaultConfig()
		WithService("serviceName")(cfg)
		assert.Equal(t, "serviceName", cfg.service)
	})

	t.Run("WithEnv", func(t *testing.T) {
		var cfg config
		WithEnv("envName")(&cfg)
		assert.Equal(t, "envName", cfg.env)
	})

	t.Run("WithEnv/override", func(t *testing.T) {
		os.Setenv("DD_ENV", "envEnv")
		defer os.Unsetenv("DD_ENV")
		cfg := defaultConfig()
		WithEnv("envName")(cfg)
		assert.Equal(t, "envName", cfg.env)
	})

	t.Run("WithVersion", func(t *testing.T) {
		var cfg config
		WithVersion("1.2.3")(&cfg)
		assert.Contains(t, cfg.tags, "version:1.2.3")
	})

	t.Run("WithVersion/override", func(t *testing.T) {
		os.Setenv("DD_VERSION", "envVersion")
		defer os.Unsetenv("DD_VERSION")
		cfg := defaultConfig()
		WithVersion("1.2.3")(cfg)
		assert.Contains(t, cfg.tags, "version:1.2.3")
	})

	t.Run("WithTags", func(t *testing.T) {
		var cfg config
		WithTags("a:1", "b:2", "c:3")(&cfg)
		assert.Contains(t, cfg.tags, "a:1")
		assert.Contains(t, cfg.tags, "b:2")
		assert.Contains(t, cfg.tags, "c:3")
	})

	t.Run("WithTags/override", func(t *testing.T) {
		os.Setenv("DD_TAGS", "env1:tag1,env2:tag2")
		defer os.Unsetenv("DD_TAGS")
		cfg := defaultConfig()
		WithTags("a:1", "b:2", "c:3")(cfg)
		assert.Contains(t, cfg.tags, "a:1")
		assert.Contains(t, cfg.tags, "b:2")
		assert.Contains(t, cfg.tags, "c:3")
		assert.Contains(t, cfg.tags, "env1:tag1")
		assert.Contains(t, cfg.tags, "env2:tag2")
	})
}

func TestEnvVars(t *testing.T) {
	t.Run("DD_ENV", func(t *testing.T) {
		os.Setenv("DD_ENV", "someEnv")
		defer os.Unsetenv("DD_ENV")
		cfg := defaultConfig()
		assert.Equal(t, "someEnv", cfg.env)
	})

	t.Run("DD_SERVICE", func(t *testing.T) {
		os.Setenv("DD_SERVICE", "someService")
		defer os.Unsetenv("DD_SERVICE")
		cfg := defaultConfig()
		assert.Equal(t, "someService", cfg.service)
	})

	t.Run("DD_VERSION", func(t *testing.T) {
		os.Setenv("DD_VERSION", "1.2.3")
		defer os.Unsetenv("DD_VERSION")
		cfg := defaultConfig()
		assert.Contains(t, cfg.tags, "version:1.2.3")
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		os.Setenv("DD_TAGS", "a:1,b:2,c:3")
		defer os.Unsetenv("DD_TAGS")
		cfg := defaultConfig()
		assert.Contains(t, cfg.tags, "a:1")
		assert.Contains(t, cfg.tags, "b:2")
		assert.Contains(t, cfg.tags, "c:3")
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		cfg := defaultConfig()
		assert := assert.New(t)
		assert.Equal(defaultAPIURL, cfg.apiURL)
		assert.Equal(defaultEnv, cfg.env)
		assert.Equal(filepath.Base(os.Args[0]), cfg.service)
		assert.Equal(len(defaultProfileTypes), len(cfg.types))
		for _, pt := range defaultProfileTypes {
			_, ok := cfg.types[pt]
			assert.True(ok)
		}
		_, ok := cfg.statsd.(*statsd.NoOpClient)
		assert.True(ok)
		assert.Equal(DefaultPeriod, cfg.period)
		assert.Equal(DefaultDuration, cfg.cpuDuration)
		assert.Equal(DefaultMutexFraction, cfg.mutexFraction)
		assert.Equal(DefaultBlockRate, cfg.blockRate)
	})
}

func TestAddProfileType(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		cfg := defaultConfig()
		_, ok := cfg.types[MutexProfile]
		assert.False(ok)
		n := len(cfg.types)
		cfg.addProfileType(MutexProfile)
		assert.Len(cfg.types, n+1)
		_, ok = cfg.types[MutexProfile]
		assert.True(ok)
	})

	t.Run("nil", func(t *testing.T) {
		var cfg config
		assert := assert.New(t)
		assert.Nil(cfg.types)
		cfg.addProfileType(MutexProfile)
		assert.Len(cfg.types, 1)
		_, ok := cfg.types[MutexProfile]
		assert.True(ok)
	})
}
