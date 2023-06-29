// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func headerTagVal(header string) (tag string) {
	tag, _ = globalconfig.HeaderTag(header)
	return tag
}

// testStatsd asserts that the given statsd.Client can successfully send metrics
// to a UDP listener located at addr.
func testStatsd(t *testing.T, cfg *config, addr string) {
	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.dogstatsdAddr)
	_, err = net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)

	client.Count("name", 1, []string{"tag"}, 1)
	require.NoError(t, client.Close())
}

func TestStatsdUDPConnect(t *testing.T) {
	defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
	os.Setenv("DD_DOGSTATSD_PORT", "8111")
	testStatsd(t, newConfig(), net.JoinHostPort(defaultHostname, "8111"))
	cfg := newConfig()
	addr := net.JoinHostPort(defaultHostname, "8111")

	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.dogstatsdAddr)
	udpaddr, err := net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", udpaddr)
	require.NoError(t, err)
	defer conn.Close()

	client.Count("name", 1, []string{"tag"}, 1)
	require.NoError(t, client.Close())

	done := make(chan struct{})
	buf := make([]byte, 4096)
	n := 0
	go func() {
		n, _ = io.ReadAtLeast(conn, buf, 1)
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(1 * time.Second):
		require.Fail(t, "No data was flushed.")
	}
	assert.Contains(t, string(buf[:n]), "name:1|c|#lang:go")
}

func TestAutoDetectStatsd(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		testStatsd(t, newConfig(), net.JoinHostPort(defaultHostname, "8125"))
	})

	t.Run("socket", func(t *testing.T) {
		if strings.HasPrefix(runtime.GOOS, "windows") {
			t.Skip("Unix only")
		}
		if testing.Short() {
			return
		}
		dir, err := ioutil.TempDir("", "socket")
		if err != nil {
			t.Fatal(err)
		}
		addr := filepath.Join(dir, "dsd.socket")

		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = addr

		uaddr, err := net.ResolveUnixAddr("unixgram", addr)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.ListenUnixgram("unixgram", uaddr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		cfg := newConfig()
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		require.Equal(t, cfg.dogstatsdAddr, "unix://"+addr)
		statsd.Count("name", 1, []string{"tag"}, 1)

		buf := make([]byte, 17)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		require.Contains(t, string(buf[:n]), "name:1|c|#lang:go")
	})

	t.Run("env", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Setenv("DD_DOGSTATSD_PORT", "8111")
		testStatsd(t, newConfig(), net.JoinHostPort(defaultHostname, "8111"))
	})

	t.Run("agent", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"statsd_port":0}`))
			}))
			defer srv.Close()
			cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8125"))
		})

		t.Run("port", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"statsd_port":8999}`))
			}))
			defer srv.Close()
			cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8999"))
		})
	})
}

func TestLoadAgentFeatures(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		t.Run("disabled", func(t *testing.T) {
			assert.Zero(t, newConfig(WithLambdaMode(true)).agent)
		})

		t.Run("unreachable", func(t *testing.T) {
			if testing.Short() {
				return
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			assert.Zero(t, newConfig(WithAgentAddr("127.9.9.9:8181")).agent)
		})

		t.Run("StatusNotFound", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			assert.Zero(t, newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://"))).agent)
		})

		t.Run("error", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte("Not JSON"))
			}))
			defer srv.Close()
			assert.Zero(t, newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://"))).agent)
		})
	})

	t.Run("OK", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"feature_flags":["a","b"],"client_drop_p0s":true,"statsd_port":8999}`))
		}))
		defer srv.Close()
		cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
		assert.True(t, cfg.agent.DropP0s)
		assert.Equal(t, cfg.agent.StatsdPort, 8999)
		assert.EqualValues(t, cfg.agent.featureFlags, map[string]struct{}{
			"a": {},
			"b": {},
		})
		assert.True(t, cfg.agent.Stats)
		assert.True(t, cfg.agent.HasFlag("a"))
		assert.True(t, cfg.agent.HasFlag("b"))
	})

	t.Run("discovery", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_TRACE_FEATURES", old) }(os.Getenv("DD_TRACE_FEATURES"))
		os.Setenv("DD_TRACE_FEATURES", "discovery")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"statsd_port":8999}`))
		}))
		defer srv.Close()
		cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
		assert.True(t, cfg.agent.DropP0s)
		assert.True(t, cfg.agent.Stats)
		assert.Equal(t, 8999, cfg.agent.StatsdPort)
	})
}

func TestTracerOptionsDefaults(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig()
		assert.Equal(float64(1), c.sampler.(RateSampler).Rate())
		assert.Regexp(`tracer\.test(\.exe)?`, c.serviceName)
		assert.Equal(&url.URL{Scheme: "http", Host: "localhost:8126"}, c.agentURL)
		assert.Equal("localhost:8125", c.dogstatsdAddr)
		assert.Nil(nil, c.httpClient)
		assert.Equal(defaultClient, c.httpClient)
	})

	t.Run("http-client", func(t *testing.T) {
		c := newConfig()
		assert.Equal(t, defaultClient, c.httpClient)
		client := &http.Client{}
		WithHTTPClient(client)(c)
		assert.Equal(t, client, c.httpClient)
	})

	t.Run("analytics", func(t *testing.T) {
		t.Run("option", func(t *testing.T) {
			defer globalconfig.SetAnalyticsRate(math.NaN())
			assert := assert.New(t)
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			tracer := newTracer(WithAnalyticsRate(0.5))
			defer tracer.Stop()
			assert.Equal(0.5, globalconfig.AnalyticsRate())
			tracer = newTracer(WithAnalytics(false))
			defer tracer.Stop()
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			tracer = newTracer(WithAnalytics(true))
			defer tracer.Stop()
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
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:8125")
		})

		t.Run("env-host", func(t *testing.T) {
			os.Setenv("DD_AGENT_HOST", "my-host")
			defer os.Unsetenv("DD_AGENT_HOST")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "my-host:8125")
		})

		t.Run("env-port", func(t *testing.T) {
			os.Setenv("DD_DOGSTATSD_PORT", "123")
			defer os.Unsetenv("DD_DOGSTATSD_PORT")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:123")
		})

		t.Run("env-both", func(t *testing.T) {
			os.Setenv("DD_AGENT_HOST", "my-host")
			os.Setenv("DD_DOGSTATSD_PORT", "123")
			defer os.Unsetenv("DD_AGENT_HOST")
			defer os.Unsetenv("DD_DOGSTATSD_PORT")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "my-host:123")
		})

		t.Run("env-env", func(t *testing.T) {
			os.Setenv("DD_ENV", "testEnv")
			defer os.Unsetenv("DD_ENV")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "testEnv", c.env)
		})

		t.Run("option", func(t *testing.T) {
			tracer := newTracer(WithDogstatsdAddress("10.1.0.12:4002"))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "10.1.0.12:4002")
		})
	})

	t.Run("env-agentAddr", func(t *testing.T) {
		os.Setenv("DD_AGENT_HOST", "trace-agent")
		defer os.Unsetenv("DD_AGENT_HOST")
		tracer := newTracer()
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(t, &url.URL{Scheme: "http", Host: "trace-agent:8126"}, c.agentURL)
	})

	t.Run("env-agentURL", func(t *testing.T) {
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "custom:1234"}, c.agentURL)
		})

		t.Run("override-env", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "testhost")
			t.Setenv("DD_TRACE_AGENT_PORT", "3333")
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "custom:1234"}, c.agentURL)
		})

		t.Run("code-override", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer := newTracer(WithAgentAddr("testhost:3333"))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "testhost:3333"}, c.agentURL)
		})
	})

	t.Run("override", func(t *testing.T) {
		os.Setenv("DD_ENV", "dev")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		env := "production"
		tracer := newTracer(WithEnv(env))
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(env, c.env)
	})

	t.Run("trace_enabled", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.True(t, c.enabled)
		})

		t.Run("override", func(t *testing.T) {
			os.Setenv("DD_TRACE_ENABLED", "false")
			defer os.Unsetenv("DD_TRACE_ENABLED")
			tracer := newTracer()
			defer tracer.Stop()
			c := tracer.config
			assert.False(t, c.enabled)
		})
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
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(float64(0.5), c.sampler.(RateSampler).Rate())
		assert.Equal(&url.URL{Scheme: "http", Host: "ddagent.consul.local:58126"}, c.agentURL)
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

	t.Run("profiler-endpoints", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c := newConfig()
			assert.True(t, c.profilerEndpoints)
		})

		t.Run("override", func(t *testing.T) {
			os.Setenv(traceprof.EndpointEnvVar, "false")
			defer os.Unsetenv(traceprof.EndpointEnvVar)
			c := newConfig()
			assert.False(t, c.profilerEndpoints)
		})
	})

	t.Run("profiler-hotspots", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c := newConfig()
			assert.True(t, c.profilerHotspots)
		})

		t.Run("override", func(t *testing.T) {
			os.Setenv(traceprof.CodeHotspotsEnvVar, "false")
			defer os.Unsetenv(traceprof.CodeHotspotsEnvVar)
			c := newConfig()
			assert.False(t, c.profilerHotspots)
		})
	})

	t.Run("env-mapping", func(t *testing.T) {
		os.Setenv("DD_SERVICE_MAPPING", "tracer.test:test2, svc:Newsvc,http.router:myRouter, noval:")
		defer os.Unsetenv("DD_SERVICE_MAPPING")

		assert := assert.New(t)
		c := newConfig()

		assert.Equal("test2", c.serviceMappings["tracer.test"])
		assert.Equal("Newsvc", c.serviceMappings["svc"])
		assert.Equal("myRouter", c.serviceMappings["http.router"])
		assert.Equal("", c.serviceMappings["noval"])
	})

	t.Run("datadog-tags", func(t *testing.T) {
		t.Run("can-set-value", func(t *testing.T) {
			os.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "200")
			defer os.Unsetenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH")
			assert := assert.New(t)
			c := newConfig()
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(200, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("default", func(t *testing.T) {
			assert := assert.New(t)
			c := newConfig()
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(128, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("clamped-to-zero", func(t *testing.T) {
			os.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "-520")
			defer os.Unsetenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH")
			assert := assert.New(t)
			c := newConfig()
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(0, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("upper-clamp", func(t *testing.T) {
			os.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "1000")
			defer os.Unsetenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH")
			assert := assert.New(t)
			c := newConfig()
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(512, p.cfg.MaxTagsHeaderLen)
		})
	})

	t.Run("attribute-schema", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c := newConfig()
			assert.Equal(t, 0, c.spanAttributeSchemaVersion)
			assert.Equal(t, false, namingschema.UseGlobalServiceName())
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c := newConfig()
			assert.Equal(t, 1, c.spanAttributeSchemaVersion)
			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})

		t.Run("options", func(t *testing.T) {
			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c := newConfig()
			WithGlobalServiceName(true)(c)

			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})
	})

	t.Run("peer-service", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c := newConfig()
			assert.Equal(t, c.peerServiceDefaultsEnabled, false)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("defaults-with-schema-v1", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			c := newConfig()
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", "true")
			t.Setenv("DD_TRACE_PEER_SERVICE_MAPPING", "old:new,old2:new2")
			c := newConfig()
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})

		t.Run("options", func(t *testing.T) {
			c := newConfig()
			WithPeerServiceDefaults(true)(c)
			WithPeerServiceMapping("old", "new")(c)
			WithPeerServiceMapping("old2", "new2")(c)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})
	})
}

func TestDefaultHTTPClient(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		// We care that whether clients are different, but doing a deep
		// comparison is overkill and can trigger the race detector, so
		// just compare the pointers.
		assert.Same(t, defaultHTTPClient(), defaultClient)
	})

	t.Run("socket", func(t *testing.T) {
		f, err := ioutil.TempFile("", "apm.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketAPM = old }(defaultSocketAPM)
		defaultSocketAPM = f.Name()
		assert.NotSame(t, defaultHTTPClient(), defaultClient)
	})
}

func TestDefaultDogstatsdAddr(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8125")
	})

	t.Run("env", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("env+socket", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
		f, err := ioutil.TempFile("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("socket", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_AGENT_HOST", old) }(os.Getenv("DD_AGENT_HOST"))
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Unsetenv("DD_AGENT_HOST")
		os.Unsetenv("DD_DOGSTATSD_PORT")
		f, err := ioutil.TempFile("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, defaultDogstatsdAddr(), "unix://"+f.Name())
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

func TestWithTraceEnabled(t *testing.T) {
	t.Run("WithTraceEnabled", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithTraceEnabled(false))
		assert.False(c.enabled)
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_ENABLED", "false")
		defer os.Unsetenv("DD_TRACE_ENABLED")
		c := newConfig()
		assert.False(c.enabled)
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_ENABLED", "false")
		defer os.Unsetenv("DD_TRACE_ENABLED")
		c := newConfig(WithTraceEnabled(true))
		assert.True(c.enabled)
	})
}

func TestWithLogStartup(t *testing.T) {
	c := newConfig()
	assert.True(t, c.logStartup)
	WithLogStartup(false)(c)
	assert.False(t, c.logStartup)
	WithLogStartup(true)(c)
	assert.True(t, c.logStartup)
}

func TestWithHeaderTags(t *testing.T) {
	t.Run("default-off", func(t *testing.T) {
		assert := assert.New(t)
		newConfig()
		assert.Equal(0, globalconfig.HeaderTagsLen())
	})
	t.Run("single-header", func(t *testing.T) {
		assert := assert.New(t)
		header := "header"
		newConfig(WithHeaderTags([]string{header}))
		assert.Equal("http.request.headers.header", headerTagVal(header))
	})

	t.Run("header-and-tag", func(t *testing.T) {
		assert := assert.New(t)
		header := "header"
		tag := "tag"
		newConfig(WithHeaderTags([]string{header + ":" + tag}))
		assert.Equal("tag", headerTagVal(header))
	})

	t.Run("multi-header", func(t *testing.T) {
		assert := assert.New(t)
		newConfig(WithHeaderTags([]string{"1header:1tag", "2header", "3header:3tag"}))
		assert.Equal("1tag", headerTagVal("1header"))
		assert.Equal("http.request.headers.2header", headerTagVal("2header"))
		assert.Equal("3tag", headerTagVal("3header"))
	})

	t.Run("normalization", func(t *testing.T) {
		assert := assert.New(t)
		newConfig(WithHeaderTags([]string{"  h!e@a-d.e*r  ", "  2header:t!a@g.  "}))
		assert.Equal(ext.HTTPRequestHeaders+".h_e_a-d_e_r", headerTagVal("h!e@a-d.e*r"))
		assert.Equal("t!a@g.", headerTagVal("2header"))
	})

	t.Run("envvar-only", func(t *testing.T) {
		os.Setenv("DD_TRACE_HEADER_TAGS", "  1header:1tag,2.h.e.a.d.e.r  ")
		defer os.Unsetenv("DD_TRACE_HEADER_TAGS")

		assert := assert.New(t)
		newConfig()

		assert.Equal("1tag", headerTagVal("1header"))
		assert.Equal(ext.HTTPRequestHeaders+".2_h_e_a_d_e_r", headerTagVal("2.h.e.a.d.e.r"))
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_HEADER_TAGS", "unexpected")
		defer os.Unsetenv("DD_TRACE_HEADER_TAGS")
		newConfig(WithHeaderTags([]string{"expected"}))
		assert.Equal(ext.HTTPRequestHeaders+".expected", headerTagVal("expected"))
		assert.Equal(1, globalconfig.HeaderTagsLen())
	})
}

func TestHostnameDisabled(t *testing.T) {
	t.Run("DisabledWithUDS", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "unix://somefakesocket")
		c := newConfig()
		assert.False(t, c.enableHostnameDetection)
	})
	t.Run("Default", func(t *testing.T) {
		c := newConfig()
		assert.True(t, c.enableHostnameDetection)
	})
	t.Run("DisableViaEnv", func(t *testing.T) {
		t.Setenv("DD_CLIENT_HOSTNAME_ENABLED", "false")
		c := newConfig()
		assert.False(t, c.enableHostnameDetection)
	})
}
