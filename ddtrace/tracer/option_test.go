// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

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
	t.Setenv("DD_DOGSTATSD_PORT", "8111")
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
		testStatsd(t, newConfig(WithAgentTimeout(2)), net.JoinHostPort(defaultHostname, "8125"))
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

		cfg := newConfig(WithAgentTimeout(2))
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		require.Equal(t, cfg.dogstatsdAddr, "unix://"+addr)
		// Ensure globalconfig also gets the auto-detected UDS address
		require.Equal(t, "unix://"+addr, globalconfig.DogstatsdAddr())
		statsd.Count("name", 1, []string{"tag"}, 1)

		buf := make([]byte, 17)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		require.Contains(t, string(buf[:n]), "name:1|c|#lang:go")
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		testStatsd(t, newConfig(), net.JoinHostPort(defaultHostname, "8111"))
	})

	t.Run("agent", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"statsd_port":0}`))
			}))
			defer srv.Close()
			cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
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
			assert.Zero(t, newConfig(WithLambdaMode(true), WithAgentTimeout(2)).agent)
		})

		t.Run("unreachable", func(t *testing.T) {
			if testing.Short() {
				return
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			assert.Zero(t, newConfig(WithAgentAddr("127.9.9.9:8181"), WithAgentTimeout(2)).agent)
		})

		t.Run("StatusNotFound", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			assert.Zero(t, newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2)).agent)
		})

		t.Run("error", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte("Not JSON"))
			}))
			defer srv.Close()
			assert.Zero(t, newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2)).agent)
		})
	})

	t.Run("OK", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"feature_flags":["a","b"],"client_drop_p0s":true,"statsd_port":8999}`))
		}))
		defer srv.Close()
		cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
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
		t.Setenv("DD_TRACE_FEATURES", "discovery")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"statsd_port":8999}`))
		}))
		defer srv.Close()
		cfg := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.True(t, cfg.agent.DropP0s)
		assert.True(t, cfg.agent.Stats)
		assert.Equal(t, 8999, cfg.agent.StatsdPort)
	})
}

// clearIntegreationsForTests clears the state of all integrations
func clearIntegrationsForTests() {
	for name, state := range contribIntegrations {
		state.imported = false
		contribIntegrations[name] = state
	}
}

func TestAgentIntegration(t *testing.T) {
	t.Run("err", func(t *testing.T) {
		assert.False(t, MarkIntegrationImported("this-integration-does-not-exist"))
	})

	// this test is run before configuring integrations and after: ensures we clean up global state
	defaultUninstrumentedTest := func(t *testing.T) {
		cfg := newConfig()
		defer clearIntegrationsForTests()

		cfg.loadContribIntegrations(nil)
		assert.Equal(t, 56, len(cfg.integrations))
		for integrationName, v := range cfg.integrations {
			assert.False(t, v.Instrumented, "integrationName=%s", integrationName)
		}
	}
	t.Run("default_before", defaultUninstrumentedTest)

	t.Run("OK import", func(t *testing.T) {
		cfg := newConfig()
		defer clearIntegrationsForTests()

		ok := MarkIntegrationImported("github.com/go-chi/chi")
		assert.True(t, ok)
		cfg.loadContribIntegrations([]*debug.Module{})
		assert.True(t, cfg.integrations["chi"].Instrumented)
	})

	t.Run("available", func(t *testing.T) {
		cfg := newConfig()
		defer clearIntegrationsForTests()

		d := debug.Module{
			Path:    "github.com/go-redis/redis",
			Version: "v1.538",
		}

		deps := []*debug.Module{&d}
		cfg.loadContribIntegrations(deps)
		assert.True(t, cfg.integrations["Redis"].Available)
		assert.Equal(t, cfg.integrations["Redis"].Version, "v1.538")
	})

	t.Run("grpc", func(t *testing.T) {
		cfg := newConfig()
		defer clearIntegrationsForTests()

		d := debug.Module{
			Path:    "google.golang.org/grpc",
			Version: "v1.520",
		}

		deps := []*debug.Module{&d}
		cfg.loadContribIntegrations(deps)
		assert.True(t, cfg.integrations["gRPC"].Available)
		assert.Equal(t, cfg.integrations["gRPC"].Version, "v1.520")
	})

	// ensure we clean up global state
	t.Run("default_after", defaultUninstrumentedTest)
}

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
}

func TestIntegrationEnabled(t *testing.T) {
	body, err := exec.Command("go", "list", "-json", "../../contrib/...").Output()
	if err != nil {
		t.Fatalf(err.Error())
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			t.Fatalf(err.Error())
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		p := strings.Replace(pkg.Dir, pkg.Root, "../..", 1)
		body, err := exec.Command("grep", "-rl", "MarkIntegrationImported", p).Output()
		if err != nil {
			t.Fatalf(err.Error())
		}
		assert.NotEqual(t, len(body), 0, "expected %s to call MarkIntegrationImported", pkg.Name)
	}
}

func compareHTTPClients(t *testing.T, x, y http.Client) {
	assert.Equal(t, x.Transport.(*http.Transport).MaxIdleConns, y.Transport.(*http.Transport).MaxIdleConns)
	assert.Equal(t, x.Transport.(*http.Transport).IdleConnTimeout, y.Transport.(*http.Transport).IdleConnTimeout)
	assert.Equal(t, x.Transport.(*http.Transport).TLSHandshakeTimeout, y.Transport.(*http.Transport).TLSHandshakeTimeout)
	assert.Equal(t, x.Transport.(*http.Transport).ExpectContinueTimeout, y.Transport.(*http.Transport).ExpectContinueTimeout)
}
func getFuncName(f any) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
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
		x := *c.httpClient
		y := *defaultHTTPClient(0)
		assert.Equal(10*time.Second, x.Timeout)
		assert.Equal(x.Timeout, y.Timeout)
		compareHTTPClients(t, x, y)
		assert.True(getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(defaultDialer.DialContext))
		assert.False(c.debug)
	})

	t.Run("http-client", func(t *testing.T) {
		c := newConfig(WithAgentTimeout(2))
		x := *c.httpClient
		y := *defaultHTTPClient(2 * time.Second)
		compareHTTPClients(t, x, y)
		assert.True(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(defaultDialer.DialContext))
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
			t.Setenv("DD_TRACE_ANALYTICS_ENABLED", "true")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newConfig()
			assert.Equal(t, 1.0, globalconfig.AnalyticsRate())
		})

		t.Run("env/off", func(t *testing.T) {
			t.Setenv("DD_TRACE_ANALYTICS_ENABLED", "kj12")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newConfig()
			assert.True(t, math.IsNaN(globalconfig.AnalyticsRate()))
		})
	})

	t.Run("debug", func(t *testing.T) {
		t.Run("option", func(t *testing.T) {
			tracer := newTracer(WithDebugMode(true))
			defer tracer.Stop()
			c := tracer.config
			assert.True(t, c.debug)
		})
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG", "true")
			c := newConfig()
			assert.True(t, c.debug)
		})
		t.Run("otel-env-debug", func(t *testing.T) {
			t.Setenv("OTEL_LOG_LEVEL", "debug")
			c := newConfig()
			assert.True(t, c.debug)
		})
		t.Run("otel-env-notdebug", func(t *testing.T) {
			// any value other than debug, does nothing
			t.Setenv("OTEL_LOG_LEVEL", "notdebug")
			c := newConfig()
			assert.False(t, c.debug)
		})
		t.Run("override-chain", func(t *testing.T) {
			assert := assert.New(t)
			// option override otel
			t.Setenv("OTEL_LOG_LEVEL", "debug")
			c := newConfig(WithDebugMode(false))
			assert.False(c.debug)
			// env override otel
			t.Setenv("DD_TRACE_DEBUG", "false")
			c = newConfig()
			assert.False(c.debug)
			// option override env
			c = newConfig(WithDebugMode(true))
			assert.True(c.debug)
		})
	})

	t.Run("dogstatsd", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:8125")
			assert.Equal(t, globalconfig.DogstatsdAddr(), "localhost:8125")
		})

		t.Run("env-host", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "my-host")
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "my-host:8125")
			assert.Equal(t, globalconfig.DogstatsdAddr(), "my-host:8125")
		})

		t.Run("env-port", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:123")
			assert.Equal(t, globalconfig.DogstatsdAddr(), "localhost:123")
		})

		t.Run("env-both", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "my-host")
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "my-host:123")
			assert.Equal(t, globalconfig.DogstatsdAddr(), "my-host:123")
		})

		t.Run("env-env", func(t *testing.T) {
			t.Setenv("DD_ENV", "testEnv")
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "testEnv", c.env)
		})

		t.Run("option", func(t *testing.T) {
			tracer := newTracer(WithDogstatsdAddress("10.1.0.12:4002"))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "10.1.0.12:4002")
			assert.Equal(t, globalconfig.DogstatsdAddr(), "10.1.0.12:4002")
		})
		t.Run("uds", func(t *testing.T) {
			assert := assert.New(t)
			dir, err := os.MkdirTemp("", "socket")
			if err != nil {
				t.Fatal("Failed to create socket")
			}
			addr := filepath.Join(dir, "dsd.socket")
			defer os.RemoveAll(addr)
			tracer := newTracer(WithDogstatsdAddress("unix://" + addr))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal("unix://"+addr, c.dogstatsdAddr)
			assert.Equal("unix://"+addr, globalconfig.DogstatsdAddr())
		})
	})

	t.Run("env-agentAddr", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "trace-agent")
		tracer := newTracer(WithAgentTimeout(2))
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(t, &url.URL{Scheme: "http", Host: "trace-agent:8126"}, c.agentURL)
	})

	t.Run("env-agentURL", func(t *testing.T) {
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "custom:1234"}, c.agentURL)
		})

		t.Run("override-env", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "testhost")
			t.Setenv("DD_TRACE_AGENT_PORT", "3333")
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer := newTracer(WithAgentTimeout(2))
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
		t.Setenv("DD_ENV", "dev")
		assert := assert.New(t)
		env := "production"
		tracer := newTracer(WithEnv(env))
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(env, c.env)
	})

	t.Run("trace_enabled", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.True(t, c.enabled.current)
			assert.Equal(t, c.enabled.cfgOrigin, telemetry.OriginDefault)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv("DD_TRACE_ENABLED", "false")
			tracer := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			c := tracer.config
			assert.False(t, c.enabled.current)
			assert.Equal(t, c.enabled.cfgOrigin, telemetry.OriginEnvVar)
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
		assert.NotNil(c.globalTags.get())
		assert.Equal("v", c.globalTags.get()["k"])
		assert.Equal("testEnv", c.env)
		assert.True(c.debug)
	})

	t.Run("env-tags", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env:test, aKey:aVal,bKey:bVal, cKey:")

		assert := assert.New(t)
		c := newConfig(WithAgentTimeout(2))

		globalTags := c.globalTags.get()
		assert.Equal("test", globalTags["env"])
		assert.Equal("aVal", globalTags["aKey"])
		assert.Equal("bVal", globalTags["bKey"])
		assert.Equal("", globalTags["cKey"])

		dVal, ok := globalTags["dKey"]
		assert.False(ok)
		assert.Equal(nil, dVal)
	})

	t.Run("profiler-endpoints", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			assert.True(t, c.profilerEndpoints)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv(traceprof.EndpointEnvVar, "false")
			c := newConfig(WithAgentTimeout(2))
			assert.False(t, c.profilerEndpoints)
		})
	})

	t.Run("profiler-hotspots", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			assert.True(t, c.profilerHotspots)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv(traceprof.CodeHotspotsEnvVar, "false")
			c := newConfig(WithAgentTimeout(2))
			assert.False(t, c.profilerHotspots)
		})
	})

	t.Run("env-mapping", func(t *testing.T) {
		t.Setenv("DD_SERVICE_MAPPING", "tracer.test:test2, svc:Newsvc,http.router:myRouter, noval:")

		assert := assert.New(t)
		c := newConfig(WithAgentTimeout(2))

		assert.Equal("test2", c.serviceMappings["tracer.test"])
		assert.Equal("Newsvc", c.serviceMappings["svc"])
		assert.Equal("myRouter", c.serviceMappings["http.router"])
		assert.Equal("", c.serviceMappings["noval"])
	})

	t.Run("datadog-tags", func(t *testing.T) {
		t.Run("can-set-value", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "200")
			assert := assert.New(t)
			c := newConfig(WithAgentTimeout(2))
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(200, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("default", func(t *testing.T) {
			assert := assert.New(t)
			c := newConfig(WithAgentTimeout(2))
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(128, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("clamped-to-zero", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "-520")
			assert := assert.New(t)
			c := newConfig(WithAgentTimeout(2))
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(0, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("upper-clamp", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "1000")
			assert := assert.New(t)
			c := newConfig(WithAgentTimeout(2))
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(512, p.cfg.MaxTagsHeaderLen)
		})
	})

	t.Run("attribute-schema", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, 0, c.spanAttributeSchemaVersion)
			assert.Equal(t, false, namingschema.UseGlobalServiceName())
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, 1, c.spanAttributeSchemaVersion)
			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})

		t.Run("options", func(t *testing.T) {
			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c := newConfig(WithAgentTimeout(2))
			WithGlobalServiceName(true)(c)

			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})
	})

	t.Run("peer-service", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, c.peerServiceDefaultsEnabled, false)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("defaults-with-schema-v1", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", "true")
			t.Setenv("DD_TRACE_PEER_SERVICE_MAPPING", "old:new,old2:new2")
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})

		t.Run("options", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			WithPeerServiceDefaults(true)(c)
			WithPeerServiceMapping("old", "new")(c)
			WithPeerServiceMapping("old2", "new2")(c)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})
	})

	t.Run("debug-open-spans", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, false, c.debugAbandonedSpans)
			assert.Equal(t, time.Duration(0), c.spanTimeout)
		})

		t.Run("debug-on", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, 10*time.Minute, c.spanTimeout)
		})

		t.Run("timeout-set", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			t.Setenv("DD_TRACE_ABANDONED_SPAN_TIMEOUT", fmt.Sprint(time.Minute))
			c := newConfig(WithAgentTimeout(2))
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, time.Minute, c.spanTimeout)
		})

		t.Run("with-function", func(t *testing.T) {
			c := newConfig(WithAgentTimeout(2))
			WithDebugSpansMode(time.Second)(c)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, time.Second, c.spanTimeout)
		})
	})

	t.Run("agent-timeout", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c := newConfig()
			assert.Equal(t, time.Duration(10*time.Second), c.httpClient.Timeout)
		})
	})
}

func TestDefaultHTTPClient(t *testing.T) {
	defTracerClient := func(timeout int) *http.Client {
		if _, err := os.Stat(internal.DefaultTraceAgentUDSPath); err == nil {
			// we have the UDS socket file, use it
			return udsClient(internal.DefaultTraceAgentUDSPath, 0)
		}
		return defaultHTTPClient(time.Second * time.Duration(timeout))
	}
	t.Run("no-socket", func(t *testing.T) {
		// We care that whether clients are different, but doing a deep
		// comparison is overkill and can trigger the race detector, so
		// just compare the pointers.

		x := *defTracerClient(2)
		y := *defaultHTTPClient(2)
		compareHTTPClients(t, x, y)
		assert.True(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(defaultDialer.DialContext))
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
		defer func(old string) { internal.DefaultTraceAgentUDSPath = old }(internal.DefaultTraceAgentUDSPath)
		internal.DefaultTraceAgentUDSPath = f.Name()
		x := *defTracerClient(2)
		y := *defaultHTTPClient(2)
		compareHTTPClients(t, x, y)
		assert.False(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(defaultDialer.DialContext))

	})
}

func TestDefaultDogstatsdAddr(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8125")
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("env+socket", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
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
		t.Setenv("DD_SERVICE", "api-intake")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("otel-env", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		t.Setenv("OTEL_SERVICE_NAME", "api-intake")
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

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=api-intake")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		t.Setenv("DD_TAGS", "service:api-intake")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("override-chain", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		assert := assert.New(t)
		globalconfig.SetServiceName("")
		c := newConfig()
		assert.Equal(c.serviceName, filepath.Base(os.Args[0]))
		assert.Equal("", globalconfig.ServiceName())

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=testService6")
		globalconfig.SetServiceName("")
		c = newConfig()
		assert.Equal(c.serviceName, "testService6")
		assert.Equal("testService6", globalconfig.ServiceName())

		t.Setenv("DD_TAGS", "service:testService")
		globalconfig.SetServiceName("")
		c = newConfig()
		assert.Equal(c.serviceName, "testService")
		assert.Equal("testService", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"))
		assert.Equal(c.serviceName, "testService2")
		assert.Equal("testService2", globalconfig.ServiceName())

		t.Setenv("OTEL_SERVICE_NAME", "testService3")
		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"))
		assert.Equal(c.serviceName, "testService3")
		assert.Equal("testService3", globalconfig.ServiceName())

		t.Setenv("DD_SERVICE", "testService4")
		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"))
		assert.Equal(c.serviceName, "testService4")
		assert.Equal("testService4", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c = newConfig(WithGlobalTag("service", "testService2"), WithService("testService5"))
		assert.Equal(c.serviceName, "testService5")
		assert.Equal("testService5", globalconfig.ServiceName())
	})
}

func TestStartWithLink(t *testing.T) {
	assert := assert.New(t)

	links := []ddtrace.SpanLink{{TraceID: 1, SpanID: 2}, {TraceID: 3, SpanID: 4}}
	span := newTracer().StartSpan("test.request", WithSpanLinks(links)).(*span)

	assert.Len(span.SpanLinks, 2)
	assert.Equal(span.SpanLinks[0].TraceID, uint64(1))
	assert.Equal(span.SpanLinks[0].SpanID, uint64(2))
	assert.Equal(span.SpanLinks[1].TraceID, uint64(3))
	assert.Equal(span.SpanLinks[1].SpanID, uint64(4))
}

func TestOtelResourceAtttributes(t *testing.T) {
	t.Run("max 10", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "tag1=val1,tag2=val2,tag3=val3,tag4=val4,tag5=val5,tag6=val6,tag7=val7,tag8=val8,tag9=val9,tag10=val10,tag11=val11,tag12=val12")
		c := newConfig()
		globalTags := c.globalTags.get()
		// runtime-id tag is added automatically, so we expect runtime-id + our first 10 tags
		assert.Len(globalTags, 11)
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
			t.Setenv("DD_TAGS", tag.in)
			c := newConfig()
			globalTags := c.globalTags.get()
			for key, expected := range tag.out {
				got, ok := globalTags[key]
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
		t.Setenv("DD_VERSION", "1.2.3")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("1.2.3", c.version)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithGlobalTag("version", "1.2.3"))
		assert.Equal("1.2.3", c.version)
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.2.3")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("1.2.3", c.version)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "version:1.2.3")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("1.2.3", c.version)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig()
		assert.Equal(c.version, "")

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.1.0")
		c = newConfig()
		assert.Equal("1.1.0", c.version)

		t.Setenv("DD_TAGS", "version:1.1.1")
		c = newConfig()
		assert.Equal("1.1.1", c.version)

		c = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.Equal("1.1.2", c.version)

		t.Setenv("DD_VERSION", "1.1.3")
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
		t.Setenv("DD_ENV", "testing")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("testing", c.env)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithGlobalTag("env", "testing"))
		assert.Equal("testing", c.env)
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=testing")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("testing", c.env)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env:testing")
		assert := assert.New(t)
		c := newConfig()

		assert.Equal("testing", c.env)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig()
		assert.Equal(c.env, "")

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=testing0")
		c = newConfig()
		assert.Equal("testing0", c.env)

		t.Setenv("DD_TAGS", "env:testing1")
		c = newConfig()
		assert.Equal("testing1", c.env)

		c = newConfig(WithGlobalTag("env", "testing2"))
		assert.Equal("testing2", c.env)

		t.Setenv("DD_ENV", "testing3")
		c = newConfig(WithGlobalTag("env", "testing2"))
		assert.Equal("testing3", c.env)

		c = newConfig(WithGlobalTag("env", "testing2"), WithEnv("testing4"))
		assert.Equal("testing4", c.env)
	})
}

func TestStatsTags(t *testing.T) {
	assert := assert.New(t)
	c := newConfig(WithService("serviceName"), WithEnv("envName"))
	defer globalconfig.SetServiceName("")
	c.hostname = "hostName"
	tags := statsTags(c)

	assert.Contains(tags, "service:serviceName")
	assert.Contains(tags, "env:envName")
	assert.Contains(tags, "host:hostName")
	assert.Contains(tags, "tracer_version:"+version.Tag)

	st := globalconfig.StatsTags()
	// all of the tracer tags except `service` and `version` should be on `st`
	assert.Len(st, len(tags)-2)
	assert.Contains(st, "env:envName")
	assert.Contains(st, "host:hostName")
	assert.Contains(st, "lang:go")
	assert.Contains(st, "lang_version:"+runtime.Version())
	assert.NotContains(st, "tracer_version:"+version.Tag)
	assert.NotContains(st, "service:serviceName")
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
		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		c := newConfig()
		assert.Equal("hostname-env", c.hostname)
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)

		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		c := newConfig(WithHostname("hostname-middleware"))
		assert.Equal("hostname-middleware", c.hostname)
	})
}

func TestWithTraceEnabled(t *testing.T) {
	t.Run("WithTraceEnabled", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithTraceEnabled(false))
		assert.False(c.enabled.current)
	})

	t.Run("otel-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		c := newConfig()
		assert.False(c.enabled.current)
	})

	t.Run("dd-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_ENABLED", "false")
		c := newConfig()
		assert.False(c.enabled.current)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		// dd env overrides otel env
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		t.Setenv("DD_TRACE_ENABLED", "true")
		c := newConfig()
		assert.True(c.enabled.current)
		// tracer option overrides dd env
		c = newConfig(WithTraceEnabled(false))
		assert.False(c.enabled.current)
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
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newConfig()
		assert.Equal(0, globalconfig.HeaderTagsLen())
	})

	t.Run("single-header", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		header := "Header"
		newConfig(WithHeaderTags([]string{header}))
		assert.Equal("http.request.headers.header", globalconfig.HeaderTag(header))
	})

	t.Run("header-and-tag", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		header := "Header"
		tag := "tag"
		newConfig(WithHeaderTags([]string{header + ":" + tag}))
		assert.Equal("tag", globalconfig.HeaderTag(header))
	})

	t.Run("multi-header", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newConfig(WithHeaderTags([]string{"1header:1tag", "2header", "3header:3tag"}))
		assert.Equal("1tag", globalconfig.HeaderTag("1header"))
		assert.Equal("http.request.headers.2header", globalconfig.HeaderTag("2header"))
		assert.Equal("3tag", globalconfig.HeaderTag("3header"))
	})

	t.Run("normalization", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newConfig(WithHeaderTags([]string{"  h!e@a-d.e*r  ", "  2header:t!a@g.  "}))
		assert.Equal(ext.HTTPRequestHeaders+".h_e_a-d_e_r", globalconfig.HeaderTag("h!e@a-d.e*r"))
		assert.Equal("t!a@g.", globalconfig.HeaderTag("2header"))
	})

	t.Run("envvar-only", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		t.Setenv("DD_TRACE_HEADER_TAGS", "  1header:1tag,2.h.e.a.d.e.r  ")

		assert := assert.New(t)
		newConfig()

		assert.Equal("1tag", globalconfig.HeaderTag("1header"))
		assert.Equal(ext.HTTPRequestHeaders+".2_h_e_a_d_e_r", globalconfig.HeaderTag("2.h.e.a.d.e.r"))
	})

	t.Run("env-override", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		t.Setenv("DD_TRACE_HEADER_TAGS", "unexpected")
		newConfig(WithHeaderTags([]string{"expected"}))
		assert.Equal(ext.HTTPRequestHeaders+".expected", globalconfig.HeaderTag("Expected"))
		assert.Equal(1, globalconfig.HeaderTagsLen())
	})

	// ensures we cleaned up global state correctly
	assert.Equal(t, 0, globalconfig.HeaderTagsLen())
}

func TestHostnameDisabled(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		c := newConfig()
		assert.False(t, c.enableHostnameDetection)
	})
	t.Run("EnableViaEnv", func(t *testing.T) {
		t.Setenv("DD_TRACE_CLIENT_HOSTNAME_COMPAT", "v1.66")
		c := newConfig()
		assert.True(t, c.enableHostnameDetection)
	})
}

func TestPartialFlushing(t *testing.T) {
	t.Run("None", func(t *testing.T) {
		c := newConfig()
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Disabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "false")
		c := newConfig()
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Default-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c := newConfig()
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, 10, c.partialFlushMinSpans)
	})
	t.Run("Enabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		c := newConfig()
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Enabled-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c := newConfig()
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, 10, c.partialFlushMinSpans)
	})
	t.Run("Enabled-SetMinSpansNegative", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "-1")
		c := newConfig()
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("WithPartialFlushOption", func(t *testing.T) {
		c := newConfig()
		WithPartialFlushing(20)(c)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, 20, c.partialFlushMinSpans)
	})
}

func TestWithStatsComputation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig()
		assert.False(c.statsComputationEnabled)
	})
	t.Run("enabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithStatsComputation(true))
		assert.True(c.statsComputationEnabled)
	})
	t.Run("disabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c := newConfig(WithStatsComputation(false))
		assert.False(c.statsComputationEnabled)
	})
	t.Run("enabled-via-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "true")
		c := newConfig()
		assert.True(c.statsComputationEnabled)
	})
	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "false")
		c := newConfig(WithStatsComputation(true))
		assert.True(c.statsComputationEnabled)
	})
}
