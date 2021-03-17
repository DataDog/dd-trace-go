// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		c := newConfig()
		assert.Equal(float64(1), c.sampler.(RateSampler).Rate())
		assert.Equal("tracer.test", c.serviceName)
		assert.Equal("localhost:8126", c.agentAddr)
		assert.Equal("localhost:8125", c.dogstatsdAddr)
		assert.Nil(nil, c.httpClient)
	})

	t.Run("analytics", func(t *testing.T) {
		t.Run("option", func(t *testing.T) {
			defer globalconfig.SetAnalyticsRate(math.NaN())
			assert := assert.New(t)
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			newTracer(WithAnalyticsRate(0.5))
			assert.Equal(0.5, globalconfig.AnalyticsRate())
			newTracer(WithAnalytics(false))
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			newTracer(WithAnalytics(true))
			assert.Equal(1., globalconfig.AnalyticsRate())
		})

		t.Run("env/on", func(t *testing.T) {
			os.Setenv("DD_TRACE_ANALYTICS_ENABLED", "true")
			defer os.Unsetenv("DD_TRACE_ANALYTICS_ENABLED")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newConfig()
			assert.Equal(t, 1.0, globalconfig.AnalyticsRate())
		})

		t.Run("env/off", func(t *testing.T) {
			os.Setenv("DD_TRACE_ANALYTICS_ENABLED", "kj12")
			defer os.Unsetenv("DD_TRACE_ANALYTICS_ENABLED")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newConfig()
			assert.True(t, math.IsNaN(globalconfig.AnalyticsRate()))
		})
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
			assert.Equal(t, "testEnv", c.env)
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
		assert.Equal(env, c.env)
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
		assert.Equal("testEnv", c.env)
		assert.True(c.debug)
	})

	t.Run("env-tags", func(t *testing.T) {
		os.Setenv("DD_TAGS", "env:test, aKey:aVal,bKey:bVal, cKey:")
		defer os.Unsetenv("DD_TAGS")

		assert := assert.New(t)
		c := newConfig()

		assert.Equal("test", c.globalTags["env"])
		assert.Equal("aVal", c.globalTags["aKey"])
		assert.Equal("bVal", c.globalTags["bKey"])
		assert.Equal("", c.globalTags["cKey"])

		dVal, ok := c.globalTags["dKey"]
		assert.False(ok)
		assert.Equal(nil, dVal)
	})

	t.Run("env-http-status-codes", func(t *testing.T) {
		os.Setenv("DD_HTTP_CLIENT_ERROR_STATUSES", "400-403,405-410")
		os.Setenv("DD_HTTP_SERVER_ERROR_STATUSES", "500,501,503")
		defer os.Unsetenv("DD_HTTP_CLIENT_ERROR_STATUSES")
		defer os.Unsetenv("DD_HTTP_SERVER_ERROR_STATUSES")

		assert := assert.New(t)
		c := newConfig()
		err := errors.New("test error")
		span := newBasicSpan("web.request")
		span.Finish(WithError(err), NoDebugStack())

		assert.Equal([]int{400, 401, 402, 403, 405, 406, 407, 408, 409, 410}, c.httpClientCodes)
		assert.Equal([]int{500, 501, 503}, c.httpServerCodes)
		assert.Equal(int32(1), span.Error)
	})
}

func TestServiceName(t *testing.T) {
	t.Run("WithServiceName", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c := newConfig(
			WithServiceName("api-intake"),
		)

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("", globalconfig.ServiceName())
	})

	t.Run("WithService", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c := newConfig(
			WithService("api-intake"),
		)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("env", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		os.Setenv("DD_SERVICE", "api-intake")
		defer os.Unsetenv("DD_SERVICE")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c := newConfig(WithGlobalTag("service", "api-intake"))
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		os.Setenv("DD_TAGS", "service:api-intake")
		defer os.Unsetenv("DD_TAGS")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		globalconfig.SetServiceName("")
		c := newConfig()
		assert.Equal(c.serviceName, filepath.Base(os.Args[0]))
		assert.Equal("", globalconfig.ServiceName())

		os.Setenv("DD_TAGS", "service:testService")
		defer os.Unsetenv("DD_TAGS")
		globalconfig.SetServiceName("")
		c = newConfig()
		assert.Equal(c.serviceName, "testService")
		assert.Equal("testService", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"))
		assert.Equal(c.serviceName, "testService2")
		assert.Equal("testService2", globalconfig.ServiceName())

		os.Setenv("DD_SERVICE", "testService3")
		defer os.Unsetenv("DD_SERVICE")
		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"))
		assert.Equal(c.serviceName, "testService3")
		assert.Equal("testService3", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"), WithService("testService4"))
		assert.Equal(c.serviceName, "testService4")
		assert.Equal("testService4", globalconfig.ServiceName())
	})
}

func TestTagSeparators(t *testing.T) {
	assert := assert.New(t)

	for _, tag := range []struct {
		in  string
		out map[string]string
	}{{
		in: "env:test aKey:aVal bKey:bVal cKey:",
		out: map[string]string{
			"env":  "test",
			"aKey": "aVal",
			"bKey": "bVal",
			"cKey": "",
		},
	},
		{
			in: "env:test,aKey:aVal,bKey:bVal,cKey:",
			out: map[string]string{
				"env":  "test",
				"aKey": "aVal",
				"bKey": "bVal",
				"cKey": "",
			},
		},
		{
			in: "env:test,aKey:aVal bKey:bVal cKey:",
			out: map[string]string{
				"env":  "test",
				"aKey": "aVal bKey:bVal cKey:",
			},
		},
		{
			in: "env:test     bKey :bVal dKey: dVal cKey:",
			out: map[string]string{
				"env":  "test",
				"bKey": "",
				"dKey": "",
				"dVal": "",
				"cKey": "",
			},
		},
		{
			in: "env :test, aKey : aVal bKey:bVal cKey:",
			out: map[string]string{
				"env":  "test",
				"aKey": "aVal bKey:bVal cKey:",
			},
		},
		{
			in: "env:keyWithA:Semicolon bKey:bVal cKey",
			out: map[string]string{
				"env":  "keyWithA:Semicolon",
				"bKey": "bVal",
				"cKey": "",
			},
		},
		{
			in: "env:keyWith:  , ,   Lots:Of:Semicolons ",
			out: map[string]string{
				"env":  "keyWith:",
				"Lots": "Of:Semicolons",
			},
		},
		{
			in: "a:b,c,d",
			out: map[string]string{
				"a": "b",
				"c": "",
				"d": "",
			},
		},
		{
			in: "a,1",
			out: map[string]string{
				"a": "",
				"1": "",
			},
		},
		{
			in:  "a:b:c:d",
			out: map[string]string{"a": "b:c:d"},
		},
	} {
		t.Run("", func(t *testing.T) {
			os.Setenv("DD_TAGS", tag.in)
			defer os.Unsetenv("DD_TAGS")
			c := newConfig()
			for key, expected := range tag.out {
				got, ok := c.globalTags[key]
				assert.True(ok, "tag not found")
				assert.Equal(expected, got)
			}
		})
	}
}

func TestVersionConfig(t *testing.T) {
	t.Run("WithServiceVersion", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(
			WithServiceVersion("1.2.3"),
		)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("env", func(t *testing.T) {
		os.Setenv("DD_VERSION", "1.2.3")
		defer os.Unsetenv("DD_VERSION")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("1.2.3", c.version)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithGlobalTag("version", "1.2.3"))
		assert.Equal("1.2.3", c.version)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		os.Setenv("DD_TAGS", "version:1.2.3")
		defer os.Unsetenv("DD_TAGS")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("1.2.3", c.version)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig()
		assert.Equal(c.version, "")

		os.Setenv("DD_TAGS", "version:1.1.1")
		defer os.Unsetenv("DD_TAGS")
		c = newConfig()
		assert.Equal("1.1.1", c.version)

		c = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.Equal("1.1.2", c.version)

		os.Setenv("DD_VERSION", "1.1.3")
		defer os.Unsetenv("DD_VERSION")
		c = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.Equal("1.1.3", c.version)

		c = newConfig(WithGlobalTag("version", "1.1.2"), WithServiceVersion("1.1.4"))
		assert.Equal("1.1.4", c.version)
	})
}

func TestEnvConfig(t *testing.T) {
	t.Run("WithEnv", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(
			WithEnv("testing"),
		)
		assert.Equal("testing", c.env)
	})

	t.Run("env", func(t *testing.T) {
		os.Setenv("DD_ENV", "testing")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("testing", c.env)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithGlobalTag("env", "testing"))
		assert.Equal("testing", c.env)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		os.Setenv("DD_TAGS", "env:testing")
		defer os.Unsetenv("DD_TAGS")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("testing", c.env)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig()
		assert.Equal(c.env, "")

		os.Setenv("DD_TAGS", "env:testing1")
		defer os.Unsetenv("DD_TAGS")
		c = newConfig()
		assert.Equal("testing1", c.env)

		c = newConfig(WithGlobalTag("env", "testing2"))
		assert.Equal("testing2", c.env)

		os.Setenv("DD_ENV", "testing3")
		defer os.Unsetenv("DD_ENV")
		c = newConfig(WithGlobalTag("env", "testing2"))
		assert.Equal("testing3", c.env)

		c = newConfig(WithGlobalTag("env", "testing2"), WithEnv("testing4"))
		assert.Equal("testing4", c.env)
	})
}

func TestStatsTags(t *testing.T) {
	assert := assert.New(t)
	c := newConfig(WithService("serviceName"), WithEnv("envName"))
	c.hostname = "hostName"
	tags := statsTags(c)

	assert.Contains(tags, "service:serviceName")
	assert.Contains(tags, "env:envName")
	assert.Contains(tags, "host:hostName")
}

func TestGlobalTag(t *testing.T) {
	var c config
	WithGlobalTag("k", "v")(&c)
	assert.Contains(t, statsTags(&c), "k:v")
}

func TestWithHostname(t *testing.T) {
	t.Run("WithHostname", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithHostname("hostname"))
		assert.Equal("hostname", c.hostname)
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		defer os.Unsetenv("DD_TRACE_SOURCE_HOSTNAME")
		c := newConfig()
		assert.Equal("hostname-env", c.hostname)
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)

		os.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		defer os.Unsetenv("DD_TRACE_SOURCE_HOSTNAME")
		c := newConfig(WithHostname("hostname-middleware"))
		assert.Equal("hostname-middleware", c.hostname)
	})
}
