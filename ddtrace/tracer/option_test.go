// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package tracer

import (
	"math"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
)

func withTransport(t transport) StartOption {
	return func(c *config) {
		c.transport = t
	}
}

func withTickChan(ch <-chan time.Time) StartOption {
	return func(c *config) {
		c.tickChan = ch
	}
}

func TestTracerOptionsDefaults(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		assert := assert.New(t)
		var c config
		defaults(&c)
		assert.Equal(float64(1), c.sampler.(RateSampler).Rate())
		assert.Equal("tracer.test", c.serviceName)
		assert.Equal("localhost:8126", c.agentAddr)
		assert.Equal("localhost:8125", c.dogstatsdAddr)
		assert.Equal(nil, c.httpRoundTripper)
	})

	t.Run("analytics", func(t *testing.T) {
		assert := assert.New(t)
		assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
		newTracer(WithAnalyticsRate(0.5))
		assert.Equal(0.5, globalconfig.AnalyticsRate())
		newTracer(WithAnalytics(false))
		assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
		newTracer(WithAnalytics(true))
		assert.Equal(1., globalconfig.AnalyticsRate())
	})

	t.Run("dogstatsd", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer := newTracer()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:8125")
		})

		t.Run("env-host", func(t *testing.T) {
			os.Setenv("DD_AGENT_HOST", "my-host")
			defer os.Unsetenv("DD_AGENT_HOST")
			tracer := newTracer()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "my-host:8125")
		})

		t.Run("env-port", func(t *testing.T) {
			os.Setenv("DD_DOGSTATSD_PORT", "123")
			defer os.Unsetenv("DD_DOGSTATSD_PORT")
			tracer := newTracer()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:123")
		})

		t.Run("env-both", func(t *testing.T) {
			os.Setenv("DD_AGENT_HOST", "my-host")
			os.Setenv("DD_DOGSTATSD_PORT", "123")
			defer os.Unsetenv("DD_AGENT_HOST")
			defer os.Unsetenv("DD_DOGSTATSD_PORT")
			tracer := newTracer()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "my-host:123")
		})

		t.Run("env-env", func(t *testing.T) {
			os.Setenv("DD_ENV", "testEnv")
			defer os.Unsetenv("DD_ENV")
			tracer := newTracer()
			c := tracer.config
			assert.Equal(t, "testEnv", c.globalTags[ext.Environment])
		})

		t.Run("option", func(t *testing.T) {
			tracer := newTracer(WithDogstatsdAddress("10.1.0.12:4002"))
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "10.1.0.12:4002")
		})
	})

	t.Run("override", func(t *testing.T) {
		os.Setenv("DD_ENV", "dev")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		env := "production"
		tracer := newTracer(WithEnv(env))
		c := tracer.config
		assert.Equal(env, c.globalTags[ext.Environment])
	})

	t.Run("other", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer(
			WithSampler(NewRateSampler(0.5)),
			WithAgentAddr("ddagent.consul.local:58126"),
			WithGlobalTag("k", "v"),
			WithDebugMode(true),
			WithEnv("testEnv"),
		)
		c := tracer.config
		assert.Equal(float64(0.5), c.sampler.(RateSampler).Rate())
		assert.Equal("ddagent.consul.local:58126", c.agentAddr)
		assert.NotNil(c.globalTags)
		assert.Equal("v", c.globalTags["k"])
		assert.Equal("testEnv", c.globalTags[ext.Environment])
		assert.True(c.debug)
	})

	t.Run("env-tags", func(t *testing.T) {
		os.Setenv("DD_TAGS", "env:test, aKey:aVal,bKey:bVal, cKey:")
		defer os.Unsetenv("DD_TAGS")

		assert := assert.New(t)
		var c config
		defaults(&c)

		assert.Equal("test", c.globalTags["env"])
		assert.Equal("aVal", c.globalTags["aKey"])
		assert.Equal("bVal", c.globalTags["bKey"])
		assert.Equal("", c.globalTags["cKey"])

		dVal, ok := c.globalTags["dKey"]
		assert.False(ok)
		assert.Equal(nil, dVal)
	})
}

func TestServiceName(t *testing.T) {
	t.Run("with-service-name", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer(
			WithServiceName("api-intake"),
		)

		assert.Equal("api-intake", tracer.config.serviceName)
		assert.Equal("", globalconfig.ServiceName())
	})

	t.Run("with-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer(
			WithService("api-intake"),
		)
		assert.Equal("api-intake", tracer.config.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("env-service", func(t *testing.T) {
		os.Setenv("DD_SERVICE", "api-intake")
		defer os.Unsetenv("DD_SERVICE")
		assert := assert.New(t)
		tracer := newTracer()

		assert.Equal("api-intake", tracer.config.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})
}
