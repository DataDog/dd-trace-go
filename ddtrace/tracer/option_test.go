// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func withTransport(t ddTransport) StartOption {
	return func(c *config) {
		c.ddTransport = t
	}
}

func withTickChan(ch <-chan time.Time) StartOption {
	return func(c *config) {
		c.tickChan = ch
	}
}

// withAgentRemoteConfig creates a mock agent server that reports remote config support.
// Use in tests that need RC to start but don't have a real agent running.
// The server is automatically closed when the test ends.
func withAgentRemoteConfig(t testing.TB) StartOption {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"endpoints":["/v0.7/config"]}`)
		default:
			// RC polling: return empty object (handled gracefully by updateState)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return func(c *config) {
		c.internalConfig.SetAgentURL(u, internalconfig.OriginCode)
	}
}

// testStatsd asserts that the given statsd.Client can successfully send metrics
// to a UDP listener located at addr.
func testStatsd(t *testing.T, cfg *config, addr string) {
	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.internalConfig.DogstatsdAddr())
	_, err = net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)

	client.Count("name", 1, []string{"tag"}, 1)
	require.NoError(t, client.Close())
}

func TestStatsdUDPConnect(t *testing.T) {
	t.Setenv("DD_DOGSTATSD_PORT", "8111")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// We simulate the agent not being able to provide the statsd port
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
	require.NoError(t, err)
	testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8111"))
	addr := net.JoinHostPort(defaultHostname, "8111")

	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.internalConfig.DogstatsdAddr())
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
		cfg, err := newTestConfig(WithAgentTimeout(2))
		require.NoError(t, err)

		testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8125"))
	})

	t.Run("socket", func(t *testing.T) {
		if strings.HasPrefix(runtime.GOOS, "windows") {
			t.Skip("Unix only")
		}
		if testing.Short() {
			return
		}
		dir, err := os.MkdirTemp("", "socket")
		if err != nil {
			t.Fatal(err)
		}
		addr := filepath.Join(dir, "dsd.socket")

		defer func(old string) { internalconfig.DefaultSocketDSDPath = old }(internalconfig.DefaultSocketDSDPath)
		internalconfig.DefaultSocketDSDPath = addr

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

		cfg, err := newTestConfig(WithAgentTimeout(2))
		assert.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		require.Equal(t, cfg.internalConfig.DogstatsdAddr(), "unix://"+addr)
		// Ensure globalconfig also gets the auto-detected UDS address
		require.Equal(t, "unix://"+addr, globalconfig.DogstatsdAddr())
		statsd.Count("name", 1, []string{"tag"}, 1)
		statsd.Flush()

		buf := make([]byte, 17)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		require.Contains(t, string(buf[:n]), "name:1|c|#lang:go")
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// We simulate the agent not being able to provide the statsd port
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
		assert.NoError(t, err)
		testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8111"))
	})

	t.Run("agent", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"endpoints": [], "config": {"statsd_port":0}}`))
			}))
			defer srv.Close()
			cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
			assert.NoError(t, err)
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8125"))
		})

		t.Run("port", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"endpoints": [], "config": {"statsd_port":8999}}`))
			}))
			defer srv.Close()
			cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			assert.NoError(t, err)
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8999"))
		})
	})
}

func TestWithStatsdClient(t *testing.T) {
	// Create a real *statsd.ClientDirect — it satisfies both statsd.ClientInterface
	// and internal.StatsdClient, which is the contract WithStatsdClient documents.
	client, err := statsd.NewDirect("localhost:8125", statsd.WithMaxMessagesPerPayload(40))
	require.NoError(t, err)
	defer client.Close()

	cfg, err := newTestConfig(WithStatsdClient(client))
	require.NoError(t, err)

	// The injected client should be used directly instead of creating a new one.
	got, err := newStatsdClient(cfg)
	require.NoError(t, err)
	assert.Equal(t, client, got, "WithStatsdClient: tracer should use the provided client")
}

func TestInternalMetricsDisabled(t *testing.T) {
	isNoop := func(c internal.StatsdClient) bool {
		_, ok := c.(*statsd.NoOpClientDirect)
		return ok
	}

	t.Run("default non-Lambda: real client", func(t *testing.T) {
		tr, err := newUnstartedTracer(WithAgentTimeout(2))
		require.NoError(t, err)
		defer tr.statsd.Close()
		require.False(t, isNoop(tr.statsd), "statsd should be real by default, got %T", tr.statsd)
	})

	t.Run("Lambda without explicit config: no-op client", func(t *testing.T) {
		// In Lambda the core config layer defaults internal metrics to off so the
		// tracer emits no statsd traffic by default.
		t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "my-function")
		tr, err := newUnstartedTracer(WithAgentTimeout(2))
		require.NoError(t, err)
		defer tr.statsd.Close()
		require.True(t, isNoop(tr.statsd), "statsd should be a no-op in Lambda by default, got %T", tr.statsd)
	})

	t.Run("Lambda with explicit opt-in: real client", func(t *testing.T) {
		// If the user explicitly enables internal metrics in Lambda, the real
		// client is used and their setting is reported with origin env_var.
		t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "my-function")
		t.Setenv("DD_TRACE_INTERNAL_METRICS_ENABLED", "true")
		tr, err := newUnstartedTracer(WithAgentTimeout(2))
		require.NoError(t, err)
		defer tr.statsd.Close()
		require.False(t, isNoop(tr.statsd), "statsd should be real when user opts in, got %T", tr.statsd)
	})
}

func TestLoadAgentFeatures(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		t.Run("disabled", func(t *testing.T) {
			cfg, err := newTestConfig(WithLambdaMode(true), WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Zero(t, cfg.agent.load())
		})

		t.Run("unreachable", func(t *testing.T) {
			cfg, err := newTestConfig(WithAgentAddr("127.0.0.1:0"))
			assert.NoError(t, err)
			assert.Zero(t, cfg.agent.load())
		})

		t.Run("StatusNotFound", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
			require.NoError(t, err)
			assert.Zero(t, cfg.agent.load())
		})

		t.Run("error", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte("Not JSON"))
			}))
			defer srv.Close()
			cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
			require.NoError(t, err)
			assert.Zero(t, cfg.agent.load())
		})
	})

	t.Run("OK", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"feature_flags":["a","b"],"client_drop_p0s":true,"obfuscation_version":2,"peer_tags":["peer.hostname"],"config": {"statsd_port":8999}}`))
		}))
		defer srv.Close()
		cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.NoError(t, err)
		a := cfg.agent.load()
		assert.True(t, a.DropP0s)
		assert.Equal(t, a.StatsdPort, 8999)
		assert.EqualValues(t, a.featureFlags, map[string]struct{}{
			"a": {},
			"b": {},
		})
		assert.True(t, a.Stats)
		assert.True(t, a.HasFlag("a"))
		assert.True(t, a.HasFlag("b"))
		assert.EqualValues(t, a.peerTags, []string{"peer.hostname"})
		assert.Equal(t, 2, a.obfuscationVersion)
		assert.False(t, a.hasTelemetryProxy)
		assert.True(t, a.reachable)
	})

	t.Run("telemetry_proxy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats","/telemetry/proxy/"],"client_drop_p0s":true}`))
		}))
		defer srv.Close()
		cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.NoError(t, err)
		a := cfg.agent.load()
		assert.True(t, a.Stats)
		assert.True(t, a.hasTelemetryProxy)
		assert.True(t, a.reachable)
	})

	t.Run("default_env", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"config": {"statsd_port":8125,"default_env":"prod"}}`))
		}))
		defer srv.Close()
		cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.NoError(t, err)
		assert.Equal(t, "prod", cfg.agent.load().defaultEnv)
	})

	t.Run("discovery", func(t *testing.T) {
		t.Setenv("DD_TRACE_FEATURES", "discovery")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"config":{"statsd_port":8999}}`))
		}))
		defer srv.Close()
		cfg, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.NoError(t, err)
		a := cfg.agent.load()
		assert.True(t, a.DropP0s)
		assert.True(t, a.Stats)
		assert.Equal(t, 8999, a.StatsdPort)
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
		cfg, err := newTestConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		cfg.loadContribIntegrations(nil)
		assert.Equal(t, 58, len(cfg.integrations))
		for integrationName, v := range cfg.integrations {
			assert.False(t, v.Instrumented, "integrationName=%s", integrationName)
		}
	}
	t.Run("default_before", defaultUninstrumentedTest)

	t.Run("OK import", func(t *testing.T) {
		cfg, err := newTestConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		ok := MarkIntegrationImported("github.com/go-chi/chi")
		assert.True(t, ok)
		cfg.loadContribIntegrations([]*debug.Module{})
		assert.True(t, cfg.integrations["chi"].Instrumented)
	})

	t.Run("available", func(t *testing.T) {
		cfg, err := newTestConfig()
		assert.Nil(t, err)
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
		cfg, err := newTestConfig()
		assert.Nil(t, err)
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
		c, err := newTestConfig()
		assert.NoError(err)
		assert.Equal(float64(1), c.sampler.Rate())
		assert.Regexp(`tracer\.test(\.exe)?`, c.internalConfig.ServiceName())
		assert.Equal(&url.URL{Scheme: "http", Host: "localhost:8126"}, c.internalConfig.RawAgentURL())
		assert.Equal("localhost:8125", c.internalConfig.DogstatsdAddr())
		assert.Nil(nil, c.httpClient)
		x := *c.httpClient
		y := *internal.DefaultHTTPClient(defaultHTTPTimeout, false)
		assert.Equal(10*time.Second, x.Timeout)
		assert.Equal(x.Timeout, y.Timeout)
		compareHTTPClients(t, x, y)
		assert.True(getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(internal.DefaultDialer(30*time.Second).DialContext))
		assert.False(c.internalConfig.Debug())
	})

	t.Run("http-client", func(t *testing.T) {
		c, err := newTestConfig(WithAgentTimeout(2))
		assert.NoError(t, err)
		x := *c.httpClient
		y := *internal.DefaultHTTPClient(2*time.Second, false)
		compareHTTPClients(t, x, y)
		assert.True(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(internal.DefaultDialer(30*time.Second).DialContext))
		client := &http.Client{}
		WithHTTPClient(client)(c)
		assert.Equal(t, client, c.httpClient)
	})

	t.Run("analytics", func(t *testing.T) {
		t.Run("option", func(t *testing.T) {
			defer globalconfig.SetAnalyticsRate(math.NaN())
			assert := assert.New(t)
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			tracer, err := newTracer(WithAnalyticsRate(0.5))
			defer tracer.Stop()
			assert.NoError(err)
			assert.Equal(0.5, globalconfig.AnalyticsRate())
			tracer, err = newTracer(WithAnalytics(false))
			assert.NoError(err)
			defer tracer.Stop()
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			tracer, err = newTracer(WithAnalytics(true))
			defer tracer.Stop()
			assert.NoError(err)
			assert.Equal(1., globalconfig.AnalyticsRate())
		})

		t.Run("env/on", func(t *testing.T) {
			t.Setenv("DD_TRACE_ANALYTICS_ENABLED", "true")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newTestConfig()
			assert.Equal(t, 1.0, globalconfig.AnalyticsRate())
		})

		t.Run("env/off", func(t *testing.T) {
			t.Setenv("DD_TRACE_ANALYTICS_ENABLED", "kj12")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newTestConfig()
			assert.True(t, math.IsNaN(globalconfig.AnalyticsRate()))
		})
	})

	t.Run("debug", func(t *testing.T) {
		t.Run("option", func(t *testing.T) {
			tracer, err := newTracer(WithDebugMode(true))
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.True(t, c.internalConfig.Debug())
		})
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG", "true")
			c, err := newTestConfig()
			assert.NoError(t, err)
			assert.True(t, c.internalConfig.Debug())
		})
		t.Run("otel-env-debug", func(t *testing.T) {
			t.Setenv("OTEL_LOG_LEVEL", "debug")
			c, err := newTestConfig()
			assert.NoError(t, err)
			assert.True(t, c.internalConfig.Debug())
		})
		t.Run("otel-env-notdebug", func(t *testing.T) {
			// any value other than debug, does nothing
			t.Setenv("OTEL_LOG_LEVEL", "notdebug")
			c, err := newTestConfig()
			assert.NoError(t, err)
			assert.False(t, c.internalConfig.Debug())
		})
		t.Run("override-chain", func(t *testing.T) {
			assert := assert.New(t)
			// option override otel
			t.Setenv("OTEL_LOG_LEVEL", "debug")
			c, err := newTestConfig(WithDebugMode(false))
			assert.NoError(err)
			assert.False(c.internalConfig.Debug())
			// env override otel
			t.Setenv("DD_TRACE_DEBUG", "false")
			c, err = newTestConfig()
			assert.NoError(err)
			assert.False(c.internalConfig.Debug())
			// option override env
			c, err = newTestConfig(WithDebugMode(true))
			assert.NoError(err)
			assert.True(c.internalConfig.Debug())
		})
	})

	t.Run("dogstatsd", func(t *testing.T) {
		// Simulate the agent (assuming no concurrency at all)
		var fail bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"feature_flags":["a","b"],"client_drop_p0s":true,"config": {"statsd_port":8125}}`))
		}))
		defer srv.Close()

		opts := []StartOption{
			WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		}

		t.Run("default", func(t *testing.T) {
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:8125", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-agent_host", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "localhost")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:8125", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-dogstatsd_host", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_HOST", "localhost")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:8125", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-port", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			tracer, err := newTracer(opts...)
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, "localhost:123", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:123", globalconfig.DogstatsdAddr())
		})

		t.Run("env-url", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_URL", "10.1.0.12:4002")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "10.1.0.12:4002", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "10.1.0.12:4002", globalconfig.DogstatsdAddr())
		})

		t.Run("env-url overrides host+port", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_URL", "10.1.0.12:4002")
			t.Setenv("DD_DOGSTATSD_HOST", "ignored")
			t.Setenv("DD_DOGSTATSD_PORT", "9999")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "10.1.0.12:4002", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "10.1.0.12:4002", globalconfig.DogstatsdAddr())
		})

		t.Run("env-port: agent not available", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			fail = true
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:123", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:123", globalconfig.DogstatsdAddr())
			fail = false
		})

		t.Run("env-all", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_HOST", "localhost")
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			t.Setenv("DD_AGENT_HOST", "other-host")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:123", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:123", globalconfig.DogstatsdAddr())
		})

		t.Run("env-all: agent not available", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_HOST", "localhost")
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			t.Setenv("DD_AGENT_HOST", "other-host")
			fail = true
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:123", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "localhost:123", globalconfig.DogstatsdAddr())
			fail = false
		})

		t.Run("option", func(t *testing.T) {
			o := make([]StartOption, 0, len(opts)+1)
			o = append(o, opts...)
			o = append(o, WithDogstatsdAddr("10.1.0.12:4002"))
			tracer, err := newTracer(o...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "10.1.0.12:4002", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "10.1.0.12:4002", globalconfig.DogstatsdAddr())
		})

		t.Run("option: agent not available", func(t *testing.T) {
			o := make([]StartOption, 0, len(opts)+1)
			o = append(o, opts...)
			fail = true
			o = append(o, WithDogstatsdAddr("10.1.0.12:4002"))
			tracer, err := newTracer(o...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "10.1.0.12:4002", c.internalConfig.DogstatsdAddr())
			assert.Equal(t, "10.1.0.12:4002", globalconfig.DogstatsdAddr())
			fail = false
		})

		t.Run("uds", func(t *testing.T) {
			if strings.HasPrefix(runtime.GOOS, "windows") {
				t.Skip("Unix only")
			}
			assert := assert.New(t)
			dir, err := os.MkdirTemp("", "socket")
			if err != nil {
				t.Fatal("Failed to create socket")
			}
			addr := filepath.Join(dir, "dsd.socket")
			defer os.RemoveAll(addr)
			tracer, err := newTracer(WithDogstatsdAddr("unix://" + addr))
			assert.NoError(err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal("unix://"+addr, c.internalConfig.DogstatsdAddr())
			assert.Equal("unix://"+addr, globalconfig.DogstatsdAddr())
		})
	})

	t.Run("env-env", func(t *testing.T) {
		t.Setenv("DD_ENV", "testEnv")
		tracer, err := newTracer(WithAgentTimeout(2))
		assert.NoError(t, err)
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(t, "testEnv", c.internalConfig.Env())
	})

	t.Run("env-agentAddr", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "localhost")
		tracer, err := newTracer(WithAgentTimeout(2))
		assert.NoError(t, err)
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:8126"}, c.internalConfig.RawAgentURL())
	})

	t.Run("env-agentURL", func(t *testing.T) {
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "127.0.0.1:1234"}, c.internalConfig.RawAgentURL())
		})

		t.Run("override-env", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "localhost")
			t.Setenv("DD_TRACE_AGENT_PORT", "3333")
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "127.0.0.1:1234"}, c.internalConfig.RawAgentURL())
		})

		t.Run("code-override", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentAddr("localhost:3333"))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:3333"}, c.internalConfig.RawAgentURL())
		})

		t.Run("code-override-full-URL", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentURL("http://localhost:3333"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:3333"}, c.internalConfig.RawAgentURL())
		})

		t.Run("code-full-UDS", func(t *testing.T) {
			tracer, err := newTracer(WithAgentURL("unix:///var/run/datadog/apm.socket"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			// Source URL is unix, effective URL is rewritten for HTTP transport
			rawAgentURL := c.internalConfig.RawAgentURL()
			effectiveAgentURL := c.internalConfig.AgentURL()
			assert.Equal(t, &url.URL{Scheme: "unix", Path: "/var/run/datadog/apm.socket"}, rawAgentURL)
			assert.Equal(t, &url.URL{Scheme: "http", Host: "UDS__var_run_datadog_apm.socket"}, effectiveAgentURL)
		})

		t.Run("code-override-full-URL-error", func(t *testing.T) {
			tp := new(log.RecordLogger)
			// Have to use UseLogger directly before tracer logger is set
			defer log.UseLogger(tp)()
			t.Setenv("DD_TRACE_AGENT_URL", "https://localhost:1234")
			tracer, err := newTracer(WithAgentURL("go://127.0.0.1:3333"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:1234"}, c.internalConfig.RawAgentURL())
			cond := func() bool {
				return strings.Contains(strings.Join(tp.Logs(), ""), "Unsupported protocol")
			}
			assert.Eventually(t, cond, 1*time.Second, 75*time.Millisecond)
		})
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("DD_ENV", "dev")
		assert := assert.New(t)
		env := "production"
		tracer, err := newTracer(WithEnv(env))
		defer tracer.Stop()
		assert.NoError(err)
		c := tracer.config
		assert.Equal(env, c.internalConfig.Env())
	})

	t.Run("trace_enabled", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			val, origin := c.internalConfig.TracingEnabledConfig().Baseline()
			assert.True(t, val)
			assert.Equal(t, telemetry.OriginDefault, origin)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv("DD_TRACE_ENABLED", "false")
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			val, origin := c.internalConfig.TracingEnabledConfig().Baseline()
			assert.False(t, val)
			assert.Equal(t, telemetry.OriginEnvVar, origin)
		})
	})

	t.Run("other", func(t *testing.T) {
		assert := assert.New(t)
		tracer, err := newTracer(
			WithSamplerRate(0.5),
			WithAgentAddr("127.0.0.1:58126"),
			WithGlobalTag("k", "v"),
			WithDebugMode(true),
			WithEnv("testEnv"),
		)
		defer tracer.Stop()
		assert.NoError(err)
		c := tracer.config
		assert.Equal(float64(0.5), c.sampler.Rate())
		assert.Equal(&url.URL{Scheme: "http", Host: "127.0.0.1:58126"}, c.internalConfig.RawAgentURL())
		assert.NotNil(c.internalConfig.GlobalTags())
		assert.Equal("v", c.internalConfig.GlobalTags()["k"])
		assert.Equal("testEnv", c.internalConfig.Env())
		assert.True(c.internalConfig.Debug())
	})

	t.Run("env-tags", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env:test, aKey:aVal,bKey:bVal, cKey:")

		assert := assert.New(t)
		c, err := newTestConfig(WithAgentTimeout(2))
		assert.NoError(err)
		globalTags := c.internalConfig.GlobalTags()
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
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.True(t, c.internalConfig.ProfilerEndpoints())
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv(traceprof.EndpointEnvVar, "false")
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.False(t, c.internalConfig.ProfilerEndpoints())
		})
	})

	t.Run("profiler-hotspots", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.True(t, c.internalConfig.ProfilerHotspotsEnabled())
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv(traceprof.CodeHotspotsEnvVar, "false")
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.False(t, c.internalConfig.ProfilerHotspotsEnabled())
		})
	})

	t.Run("env-mapping", func(t *testing.T) {
		t.Setenv("DD_SERVICE_MAPPING", "tracer.test:test2, svc:Newsvc,http.router:myRouter, noval:")

		assert := assert.New(t)
		c, err := newTestConfig(WithAgentTimeout(2))

		assert.NoError(err)
		serviceMappings := c.internalConfig.ServiceMappings()
		assert.Equal("test2", serviceMappings["tracer.test"])
		assert.Equal("Newsvc", serviceMappings["svc"])
		assert.Equal("myRouter", serviceMappings["http.router"])
		assert.Equal("", serviceMappings["noval"])
	})

	t.Run("datadog-tags", func(t *testing.T) {
		t.Run("can-set-value", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "200")
			assert := assert.New(t)
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(200, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("default", func(t *testing.T) {
			assert := assert.New(t)
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(512, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("clamped-to-zero", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "-520")
			assert := assert.New(t)
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(0, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("upper-clamp", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "1000")
			assert := assert.New(t)
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(512, p.cfg.MaxTagsHeaderLen)
		})
	})

	t.Run("peer-service", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, c.internalConfig.PeerServiceDefaultsEnabled(), false)
			assert.Empty(t, c.internalConfig.PeerServiceMappings())
		})

		t.Run("defaults-with-schema-v1", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, c.internalConfig.PeerServiceDefaultsEnabled(), true)
			assert.Empty(t, c.internalConfig.PeerServiceMappings())
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", "true")
			t.Setenv("DD_TRACE_PEER_SERVICE_MAPPING", "old:new,old2:new2")
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, c.internalConfig.PeerServiceDefaultsEnabled(), true)
			assert.Equal(t, c.internalConfig.PeerServiceMappings(), map[string]string{"old": "new", "old2": "new2"})
		})

		t.Run("options", func(t *testing.T) {
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			WithPeerServiceDefaults(true)(c)
			WithPeerServiceMapping("old", "new")(c)
			WithPeerServiceMapping("old2", "new2")(c)
			assert.Equal(t, c.internalConfig.PeerServiceDefaultsEnabled(), true)
			assert.Equal(t, c.internalConfig.PeerServiceMappings(), map[string]string{"old": "new", "old2": "new2"})
		})
	})

	t.Run("debug-open-spans", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, false, c.internalConfig.DebugAbandonedSpans())
			assert.Equal(t, 10*time.Minute, c.internalConfig.SpanTimeout())
		})

		t.Run("debug-on", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, true, c.internalConfig.DebugAbandonedSpans())
			assert.Equal(t, 10*time.Minute, c.internalConfig.SpanTimeout())
		})

		t.Run("timeout-set", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			t.Setenv("DD_TRACE_ABANDONED_SPAN_TIMEOUT", fmt.Sprint(time.Minute))
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, true, c.internalConfig.DebugAbandonedSpans())
			assert.Equal(t, time.Minute, c.internalConfig.SpanTimeout())
		})

		t.Run("with-function", func(t *testing.T) {
			c, err := newTestConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			WithDebugSpansMode(time.Second)(c)
			assert.Equal(t, true, c.internalConfig.DebugAbandonedSpans())
			assert.Equal(t, time.Second, c.internalConfig.SpanTimeout())
		})
	})

	t.Run("agent-timeout", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newTestConfig()
			assert.NoError(t, err)
			assert.Equal(t, time.Duration(10*time.Second), c.httpClient.Timeout)
		})
	})

	t.Run("trace-retries", func(t *testing.T) {
		c, err := newTestConfig()
		assert.NoError(t, err)
		assert.Equal(t, 0, c.internalConfig.SendRetries())
		assert.Equal(t, time.Millisecond, c.internalConfig.RetryInterval())
	})
}

func TestTraceRetry(t *testing.T) {
	t.Run("sendRetries", func(t *testing.T) {
		c, err := newTestConfig(WithSendRetries(10))
		assert.NoError(t, err)
		assert.Equal(t, 10, c.internalConfig.SendRetries())
	})
	t.Run("retryInterval", func(t *testing.T) {
		c, err := newTestConfig(WithRetryInterval(10))
		assert.NoError(t, err)
		assert.Equal(t, 10*time.Second, c.internalConfig.RetryInterval())
	})
}

func TestDefaultHTTPClient(t *testing.T) {
	defTracerClient := func(timeout int) *http.Client {
		if _, err := os.Stat(internal.DefaultTraceAgentUDSPath); err == nil {
			// we have the UDS socket file, use it
			return internal.UDSClient(internal.DefaultTraceAgentUDSPath, 0)
		}
		return internal.DefaultHTTPClient(time.Second*time.Duration(timeout), false)
	}
	t.Run("no-socket", func(t *testing.T) {
		// We care that whether clients are different, but doing a deep
		// comparison is overkill and can trigger the race detector, so
		// just compare the pointers.

		x := *defTracerClient(2)
		y := *internal.DefaultHTTPClient(2, false)
		compareHTTPClients(t, x, y)
		assert.True(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(internal.DefaultDialer(30*time.Second).DialContext))
	})

	t.Run("socket", func(t *testing.T) {
		f, err := os.CreateTemp("", "apm.socket")
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
		y := *internal.DefaultHTTPClient(2, false)
		compareHTTPClients(t, x, y)
		assert.False(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(internal.DefaultDialer(30*time.Second).DialContext))

	})
}

func TestServiceName(t *testing.T) {
	t.Run("WithService", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newTestConfig(
			WithService("api-intake"),
		)
		assert.NoError(err)
		assert.Equal("api-intake", c.internalConfig.ServiceName())
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("env", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		t.Setenv("DD_SERVICE", "api-intake")
		assert := assert.New(t)
		c, err := newTestConfig()

		assert.NoError(err)
		assert.Equal("api-intake", c.internalConfig.ServiceName())
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("otel-env", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		t.Setenv("OTEL_SERVICE_NAME", "api-intake")
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)

		assert.Equal("api-intake", c.internalConfig.ServiceName())
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newTestConfig(WithGlobalTag("service", "api-intake"))
		assert.NoError(err)
		assert.Equal("api-intake", c.internalConfig.ServiceName())
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=api-intake")
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)

		assert.Equal("api-intake", c.internalConfig.ServiceName())
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		t.Setenv("DD_TAGS", "service:api-intake")
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)

		assert.Equal("api-intake", c.internalConfig.ServiceName())
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("override-chain", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		assert := assert.New(t)
		globalconfig.SetServiceName("")
		c, err := newTestConfig()
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), filepath.Base(os.Args[0]))
		assert.Equal("", globalconfig.ServiceName())

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=testService6")
		globalconfig.SetServiceName("")
		c, err = newTestConfig()
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), "testService6")
		assert.Equal("testService6", globalconfig.ServiceName())

		t.Setenv("DD_TAGS", "service:testService")
		globalconfig.SetServiceName("")
		c, err = newTestConfig()
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), "testService")
		assert.Equal("testService", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c, err = newTestConfig(WithGlobalTag("service", "testService2"))
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), "testService2")
		assert.Equal("testService2", globalconfig.ServiceName())

		t.Setenv("OTEL_SERVICE_NAME", "testService3")
		globalconfig.SetServiceName("")
		c, err = newTestConfig(WithGlobalTag("service", "testService2"))
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), "testService3")
		assert.Equal("testService3", globalconfig.ServiceName())

		t.Setenv("DD_SERVICE", "testService4")
		globalconfig.SetServiceName("")
		c, err = newTestConfig(WithGlobalTag("service", "testService2"), WithService("testService4"))
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), "testService4")
		assert.Equal("testService4", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c, err = newTestConfig(WithGlobalTag("service", "testService2"), WithService("testService5"))
		assert.NoError(err)
		assert.Equal(c.internalConfig.ServiceName(), "testService5")
		assert.Equal("testService5", globalconfig.ServiceName())
	})
}

func TestServiceNameProcessTag(t *testing.T) {
	setup := func(t *testing.T) {
		t.Helper()
		internalconfig.SetUseFreshConfig(true)
		t.Cleanup(func() {
			internalconfig.SetUseFreshConfig(false)
			processtags.Reload()
		})
		processtags.Reload()
	}

	t.Run("no DD_SERVICE defaults to binary name and sets svc.auto", func(t *testing.T) {
		setup(t)
		defer globalconfig.SetServiceName("")
		_, err := newTestConfig()
		require.NoError(t, err)
		tags := processtags.GlobalTags()
		assert.Contains(t, tags.String(), "svc.auto:"+filepath.Base(os.Args[0]))
		assert.NotContains(t, tags.String(), "svc.user")
	})

	t.Run("DD_SERVICE set produces svc.user:true", func(t *testing.T) {
		setup(t)
		t.Setenv("DD_SERVICE", "my-service")
		defer globalconfig.SetServiceName("")
		_, err := newTestConfig()
		require.NoError(t, err)
		tags := processtags.GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
	})

	t.Run("WithService produces svc.user:true", func(t *testing.T) {
		setup(t)
		defer globalconfig.SetServiceName("")
		_, err := newTestConfig(WithService("my-service"))
		require.NoError(t, err)
		tags := processtags.GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
	})

	t.Run("WithGlobalTag service produces svc.user:true", func(t *testing.T) {
		setup(t)
		defer globalconfig.SetServiceName("")
		_, err := newTestConfig(WithGlobalTag("service", "my-service"))
		require.NoError(t, err)
		tags := processtags.GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
	})

	t.Run("OTEL_SERVICE_NAME produces svc.user:true", func(t *testing.T) {
		setup(t)
		t.Setenv("OTEL_SERVICE_NAME", "my-service")
		defer globalconfig.SetServiceName("")
		_, err := newTestConfig()
		require.NoError(t, err)
		tags := processtags.GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
	})

	t.Run("DD_TAGS service produces svc.user:true", func(t *testing.T) {
		setup(t)
		t.Setenv("DD_TAGS", "service:my-service")
		defer globalconfig.SetServiceName("")
		_, err := newTestConfig()
		require.NoError(t, err)
		tags := processtags.GlobalTags()
		assert.Contains(t, tags.String(), "svc.user:true")
		assert.NotContains(t, tags.String(), "svc.auto")
	})
}

func TestStartWithLink(t *testing.T) {
	assert := assert.New(t)

	links := []SpanLink{{TraceID: 1, SpanID: 2}, {TraceID: 3, SpanID: 4}}
	tracer, err := newTracer()
	assert.NoError(err)
	defer tracer.Stop()

	span := tracer.StartSpan("test.request", WithSpanLinks(links))
	assert.Len(span.spanLinks, 2)
	assert.Equal(span.spanLinks[0].TraceID, uint64(1))
	assert.Equal(span.spanLinks[0].SpanID, uint64(2))
	assert.Equal(span.spanLinks[1].TraceID, uint64(3))
	assert.Equal(span.spanLinks[1].SpanID, uint64(4))
}

func TestOtelResourceAtttributes(t *testing.T) {
	t.Run("max 10", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "tag1=val1,tag2=val2,tag3=val3,tag4=val4,tag5=val5,tag6=val6,tag7=val7,tag8=val8,tag9=val9,tag10=val10,tag11=val11,tag12=val12")
		c, err := newTestConfig()
		assert.NoError(err)
		globalTags := c.internalConfig.GlobalTags()
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
			c, err := newTestConfig()
			assert.NoError(err)
			globalTags := c.internalConfig.GlobalTags()
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
		c, err := newTestConfig(
			WithServiceVersion("1.2.3"),
		)
		assert.NoError(err)
		assert.Equal("1.2.3", c.internalConfig.Version())
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_VERSION", "1.2.3")
		assert := assert.New(t)
		c, err := newTestConfig()

		assert.NoError(err)
		assert.Equal("1.2.3", c.internalConfig.Version())
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(WithGlobalTag("version", "1.2.3"))
		assert.NoError(err)
		assert.Equal("1.2.3", c.internalConfig.Version())
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.2.3")
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)

		assert.Equal("1.2.3", c.internalConfig.Version())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "version:1.2.3")
		assert := assert.New(t)
		c, err := newTestConfig()

		assert.NoError(err)
		assert.Equal("1.2.3", c.internalConfig.Version())
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)
		assert.Equal(c.internalConfig.Version(), "")

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.1.0")
		c, err = newTestConfig()
		assert.NoError(err)
		assert.Equal("1.1.0", c.internalConfig.Version())

		t.Setenv("DD_TAGS", "version:1.1.1")
		c, err = newTestConfig()
		assert.NoError(err)
		assert.Equal("1.1.1", c.internalConfig.Version())

		c, err = newTestConfig(WithGlobalTag("version", "1.1.2"))
		assert.NoError(err)
		assert.Equal("1.1.2", c.internalConfig.Version())

		t.Setenv("DD_VERSION", "1.1.3")
		c, err = newTestConfig(WithGlobalTag("version", "1.1.2"))
		assert.NoError(err)
		assert.Equal("1.1.3", c.internalConfig.Version())

		c, err = newTestConfig(WithGlobalTag("version", "1.1.2"), WithServiceVersion("1.1.4"))
		assert.NoError(err)
		assert.Equal("1.1.4", c.internalConfig.Version())
	})
}

func TestEnvConfig(t *testing.T) {
	t.Run("WithEnv", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(
			WithEnv("testing"),
		)
		assert.NoError(err)
		assert.Equal("testing", c.internalConfig.Env())
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_ENV", "testing")
		assert := assert.New(t)
		c, err := newTestConfig()

		assert.NoError(err)
		assert.Equal("testing", c.internalConfig.Env())
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(WithGlobalTag("env", "testing"))
		assert.NoError(err)
		assert.Equal("testing", c.internalConfig.Env())
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=testing")
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)

		assert.Equal("testing", c.internalConfig.Env())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env:testing")
		assert := assert.New(t)
		c, err := newTestConfig()

		assert.NoError(err)
		assert.Equal("testing", c.internalConfig.Env())
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)
		assert.Equal(c.internalConfig.Env(), "")

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=testing0")
		c, err = newTestConfig()
		assert.NoError(err)
		assert.Equal("testing0", c.internalConfig.Env())

		t.Setenv("DD_TAGS", "env:testing1")
		c, err = newTestConfig()
		assert.NoError(err)
		assert.Equal("testing1", c.internalConfig.Env())

		c, err = newTestConfig(WithGlobalTag("env", "testing2"))
		assert.NoError(err)
		assert.Equal("testing2", c.internalConfig.Env())

		t.Setenv("DD_ENV", "testing3")
		c, err = newTestConfig(WithGlobalTag("env", "testing2"))
		assert.NoError(err)
		assert.Equal("testing3", c.internalConfig.Env())

		c, err = newTestConfig(WithGlobalTag("env", "testing2"), WithEnv("testing4"))
		assert.NoError(err)
		assert.Equal("testing4", c.internalConfig.Env())
	})
}

func TestStatsTags(t *testing.T) {
	setupProcessTags := func(t *testing.T, enabled string) {
		t.Helper()
		t.Cleanup(processtags.Reload)
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", enabled)
		processtags.Reload()
	}

	t.Run("process tags are shared with contrib stats tags", func(t *testing.T) {
		assert := assert.New(t)
		setupProcessTags(t, "true")
		t.Cleanup(func() {
			globalconfig.SetServiceName("")
			globalconfig.SetStatsTags(nil)
		})
		c, err := newTestConfig(WithService("serviceName"), WithEnv("envName"))
		assert.NoError(err)
		c.internalConfig.SetHostname("hostName", telemetry.OriginCode)
		tags := statsTags(c)

		assert.Contains(tags, "service:serviceName")
		assert.Contains(tags, "env:envName")
		assert.Contains(tags, "host:hostName")
		assert.Contains(tags, ext.RuntimeID+":"+globalconfig.RuntimeID())
		processTags := processtags.GlobalTags().Slice()
		require.NotEmpty(t, processTags)
		for _, tag := range processTags {
			assert.Contains(tags, tag)
		}
		assert.Contains(tags, "tracer_version:"+version.Tag)

		st := globalconfig.StatsTags()
		assert.Len(st, len(tags)-2)
		assert.Contains(st, "env:envName")
		assert.Contains(st, "host:hostName")
		assert.Contains(st, "lang:go")
		assert.Contains(st, "lang_version:"+runtime.Version())
		assert.Contains(st, ext.RuntimeID+":"+globalconfig.RuntimeID())
		for _, tag := range processTags {
			assert.Contains(st, tag)
		}
		assert.NotContains(st, "tracer_version:"+version.Tag)
		assert.NotContains(st, "service:serviceName")
	})

	t.Run("process tags collection disabled", func(t *testing.T) {
		assert := assert.New(t)
		setupProcessTags(t, "false")
		t.Cleanup(func() {
			globalconfig.SetServiceName("")
			globalconfig.SetStatsTags(nil)
		})
		c, err := newTestConfig(WithService("serviceName"), WithEnv("envName"))
		assert.NoError(err)
		c.internalConfig.SetHostname("hostName", telemetry.OriginCode)
		tags := statsTags(c)

		assert.Nil(processtags.GlobalTags())
		assert.Contains(tags, "service:serviceName")
		assert.Contains(tags, "tracer_version:"+version.Tag)
		st := globalconfig.StatsTags()
		assert.Len(st, len(tags)-2)
		for _, tag := range append(tags, st...) {
			assert.Falsef(strings.HasPrefix(tag, "entrypoint."), "unexpected process tag %q", tag)
			assert.Falsef(strings.HasPrefix(tag, "svc."), "unexpected process tag %q", tag)
		}
		assert.NotContains(st, "tracer_version:"+version.Tag)
		assert.NotContains(st, "service:serviceName")
	})
}

func TestGlobalTag(t *testing.T) {
	c, err := newTestConfig()
	assert.NoError(t, err)
	WithGlobalTag("k", "v")(c)
	assert.Contains(t, statsTags(c), "k:v")
}

func TestWithHostname(t *testing.T) {
	t.Run("WithHostname", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(WithHostname("hostname"))
		assert.NoError(err)
		assert.Equal("hostname", c.internalConfig.Hostname())
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		c, err := newTestConfig()
		assert.NoError(err)
		assert.Equal("hostname-env", c.internalConfig.Hostname())
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)

		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		c, err := newTestConfig(WithHostname("hostname-middleware"))
		assert.NoError(err)
		assert.Equal("hostname-middleware", c.internalConfig.Hostname())
	})
}

func TestWithTraceEnabled(t *testing.T) {
	t.Run("WithTraceEnabled", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(WithTraceEnabled(false))
		assert.NoError(err)
		assert.False(c.internalConfig.TracingEnabled())
	})

	t.Run("dd-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_ENABLED", "false")
		c, err := newTestConfig()
		assert.NoError(err)
		assert.False(c.internalConfig.TracingEnabled())
	})

	t.Run("option-overrides-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_ENABLED", "true")
		c, err := newTestConfig(WithTraceEnabled(false))
		assert.NoError(err)
		assert.False(c.internalConfig.TracingEnabled())
	})
}

func TestWithLogStartup(t *testing.T) {
	c, err := newTestConfig()
	assert.NoError(t, err)
	assert.True(t, c.internalConfig.LogStartup())
	WithLogStartup(false)(c)
	assert.False(t, c.internalConfig.LogStartup())
	WithLogStartup(true)(c)
	assert.True(t, c.internalConfig.LogStartup())
}

func TestWithHeaderTags(t *testing.T) {
	t.Run("default-off", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newTestConfig()
		assert.Equal(0, globalconfig.HeaderTagsLen())
	})

	t.Run("single-header", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		header := "Header"
		newTestConfig(WithHeaderTags([]string{header}))
		assert.Equal("http.request.headers.header", globalconfig.HeaderTag(header))
	})

	t.Run("header-and-tag", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		header := "Header"
		tag := "tag"
		newTestConfig(WithHeaderTags([]string{header + ":" + tag}))
		assert.Equal("tag", globalconfig.HeaderTag(header))
	})

	t.Run("multi-header", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newTestConfig(WithHeaderTags([]string{"1header:1tag", "2header", "3header:3tag"}))
		assert.Equal("1tag", globalconfig.HeaderTag("1header"))
		assert.Equal("http.request.headers.2header", globalconfig.HeaderTag("2header"))
		assert.Equal("3tag", globalconfig.HeaderTag("3header"))
	})

	t.Run("normalization", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newTestConfig(WithHeaderTags([]string{"  h!e@a-d.e*r  ", "  2header:t!a@g.  "}))
		assert.Equal(ext.HTTPRequestHeaders+".h_e_a-d_e_r", globalconfig.HeaderTag("h!e@a-d.e*r"))
		assert.Equal("t!a@g.", globalconfig.HeaderTag("2header"))
	})

	t.Run("envvar-only", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		t.Setenv("DD_TRACE_HEADER_TAGS", "  1header:1tag,2.h.e.a.d.e.r  ")

		assert := assert.New(t)
		newTestConfig()

		assert.Equal("1tag", globalconfig.HeaderTag("1header"))
		assert.Equal(ext.HTTPRequestHeaders+".2_h_e_a_d_e_r", globalconfig.HeaderTag("2.h.e.a.d.e.r"))
	})

	t.Run("envvar-invalid", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		t.Setenv("DD_TRACE_HEADER_TAGS", "header1:")

		assert := assert.New(t)
		newTestConfig()

		assert.Equal(0, globalconfig.HeaderTagsLen())
	})

	t.Run("envvar-partially-invalid", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		t.Setenv("DD_TRACE_HEADER_TAGS", "header1,header2:")

		assert := assert.New(t)
		newTestConfig()

		assert.Equal(1, globalconfig.HeaderTagsLen())
		assert.Equal(ext.HTTPRequestHeaders+".header1", globalconfig.HeaderTag("Header1"))
	})

	t.Run("env-override", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		t.Setenv("DD_TRACE_HEADER_TAGS", "unexpected")
		newTestConfig(WithHeaderTags([]string{"expected"}))
		assert.Equal(ext.HTTPRequestHeaders+".expected", globalconfig.HeaderTag("Expected"))
		assert.Equal(1, globalconfig.HeaderTagsLen())
	})

	// ensures we cleaned up global state correctly
	assert.Equal(t, 0, globalconfig.HeaderTagsLen())
}

func TestPartialFlushing(t *testing.T) {
	partialFlushMinSpansDefault := 1000
	t.Run("None", func(t *testing.T) {
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.False(t, enabled)
		assert.Equal(t, partialFlushMinSpansDefault, min)
	})
	t.Run("Disabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "false")
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.False(t, enabled)
		assert.Equal(t, partialFlushMinSpansDefault, min)
	})
	t.Run("Default-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.False(t, enabled)
		assert.Equal(t, 10, min)
	})
	t.Run("Enabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.True(t, enabled)
		assert.Equal(t, partialFlushMinSpansDefault, min)
	})
	t.Run("Enabled-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.True(t, enabled)
		assert.Equal(t, 10, min)
	})
	t.Run("Enabled-SetMinSpansNegative", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "-1")
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.True(t, enabled)
		assert.Equal(t, partialFlushMinSpansDefault, min)
	})
	t.Run("Enabled-SetMinSpansAboveMax", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", strconv.Itoa(internalconfig.TraceMaxSize))
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.True(t, enabled)
		assert.Equal(t, partialFlushMinSpansDefault, min)
	})
	t.Run("Enabled-SetMinSpans0", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "0")
		c, err := newTestConfig()
		assert.NoError(t, err)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.True(t, enabled)
		assert.Equal(t, partialFlushMinSpansDefault, min)
	})
	t.Run("WithPartialFlushOption", func(t *testing.T) {
		c, err := newTestConfig()
		assert.NoError(t, err)
		WithPartialFlushing(20)(c)
		enabled, min := c.internalConfig.PartialFlushEnabled()
		assert.True(t, enabled)
		assert.Equal(t, 20, min)
	})
}

func TestWithStatsComputation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig()
		assert.NoError(err)
		assert.True(c.internalConfig.StatsComputationEnabled())
	})
	t.Run("enabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(WithStatsComputation(true))
		assert.NoError(err)
		assert.True(c.internalConfig.StatsComputationEnabled())
	})
	t.Run("disabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newTestConfig(WithStatsComputation(false))
		assert.NoError(err)
		assert.False(c.internalConfig.StatsComputationEnabled())
		assert.Equal(traceProtocolV04, c.internalConfig.TraceProtocol())
	})
	t.Run("enabled-via-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "true")
		c, err := newTestConfig()
		assert.NoError(err)
		assert.True(c.internalConfig.StatsComputationEnabled())
	})
	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "false")
		c, err := newTestConfig(WithStatsComputation(true))
		assert.NoError(err)
		assert.True(c.internalConfig.StatsComputationEnabled())
	})
}

func TestWithStartSpanConfig(t *testing.T) {
	var (
		assert  = assert.New(t)
		service = "service"
		parent  = newSpan("", service, "", 0, 1, 2)
		spanID  = uint64(123)
		tm, _   = time.Parse(time.RFC3339, "2019-01-01T00:00:00Z")
	)
	cfg := NewStartSpanConfig(
		ChildOf(parent.Context()),
		Measured(),
		ResourceName("resource"),
		ServiceName(service),
		SpanType(ext.SpanTypeWeb),
		StartTime(tm),
		Tag("key", "value"),
		WithSpanID(spanID),
		withContext(context.Background()),
	)
	// It's difficult to test the context was used to initialize the span
	// in a meaningful way, so we just check it was set in the SpanConfig.
	assert.Equal(cfg.Context, cfg.Context)

	tracer, err := newTracer()
	defer tracer.Stop()
	assert.NoError(err)

	s := tracer.StartSpan("test", WithStartSpanConfig(cfg))
	defer s.Finish()
	assert.Equal(float64(1), s.metrics[keyMeasured])
	v, _ := s.meta.Get("key")
	assert.Equal("value", v)
	assert.Equal(parent.Context().SpanID(), s.parentID)
	assert.Equal(parent.Context().TraceID(), s.Context().TraceID())
	assert.Equal("resource", s.resource)
	assert.Equal(service, s.service)
	assert.Equal(spanID, s.spanID)
	assert.Equal(ext.SpanTypeWeb, s.spanType)
	assert.Equal(tm.UnixNano(), s.start)
}

func TestWithTags(t *testing.T) {
	t.Run("sets_tags", func(t *testing.T) {
		var assert = assert.New(t)
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(err)

		s := tracer.StartSpan("test", WithTags(map[string]any{
			"key1": "value1",
			"key2": "value2",
		}))
		defer s.Finish()
		v, _ := s.meta.Get("key1")
		assert.Equal("value1", v)
		v, _ = s.meta.Get("key2")
		assert.Equal("value2", v)
	})

	t.Run("merges_with_existing_tags", func(t *testing.T) {
		var assert = assert.New(t)
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(err)

		s := tracer.StartSpan("test",
			Tag("key1", "from_tag"),
			WithTags(map[string]any{
				"key1": "from_with_tags",
				"key2": "value2",
			}),
		)
		defer s.Finish()
		v, _ := s.meta.Get("key1")
		assert.Equal("from_with_tags", v)
		v, _ = s.meta.Get("key2")
		assert.Equal("value2", v)
	})

	t.Run("does_not_mutate_base_config", func(t *testing.T) {
		var assert = assert.New(t)
		base := NewStartSpanConfig(
			Tag("static1", "s1"),
			Tag("static2", "s2"),
		)

		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(err)

		s := tracer.StartSpan("test",
			WithTags(map[string]any{"dynamic": "d1"}),
			WithStartSpanConfig(base),
		)
		defer s.Finish()
		v, _ := s.meta.Get("dynamic")
		assert.Equal("d1", v)
		v, _ = s.meta.Get("static1")
		assert.Equal("s1", v)
		v, _ = s.meta.Get("static2")
		assert.Equal("s2", v)

		// base.Tags must remain untouched by the per-call dynamic tag.
		assert.Len(base.Tags, 2)
		_, ok := base.Tags["dynamic"]
		assert.False(ok)
	})

	t.Run("does_not_mutate_input_map", func(t *testing.T) {
		var assert = assert.New(t)
		base := NewStartSpanConfig(
			Tag("static1", "s1"),
		)

		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(err)

		dynamic := map[string]any{"dynamic": "d1"}
		s := tracer.StartSpan("test",
			WithTags(dynamic),
			WithStartSpanConfig(base),
			Tag("extra", "e1"),
		)
		defer s.Finish()

		// The map passed to WithTags must remain untouched by later
		// options (WithStartSpanConfig, Tag) in the same option list, so
		// callers can safely reuse it across spans.
		assert.Len(dynamic, 1)
		_, ok := dynamic["static1"]
		assert.False(ok)
		_, ok = dynamic["extra"]
		assert.False(ok)
	})
}

func TestNewFinishConfig(t *testing.T) {
	var (
		assert = assert.New(t)
		now    = time.Now()
		err    = errors.New("error")
	)
	cfg := NewFinishConfig(
		FinishTime(now),
		WithError(err),
		StackFrames(10, 0),
		NoDebugStack(),
	)
	assert.True(cfg.NoDebugStack)
	assert.Equal(now, cfg.FinishTime)
	assert.Equal(err, cfg.Error)
	assert.Equal(uint(10), cfg.StackFrames)
	assert.Equal(uint(0), cfg.SkipStackFrames)
}

func TestWithStartSpanConfigNonEmptyTags(t *testing.T) {
	var (
		assert = assert.New(t)
	)
	cfg := NewStartSpanConfig(
		Tag("key", "value"),
		Tag("k2", "should_override"),
	)

	tracer, err := newTracer()
	defer tracer.Stop()
	assert.NoError(err)

	s := tracer.StartSpan(
		"test",
		Tag("k2", "v2"),
		WithStartSpanConfig(cfg),
		Tag("key", "after_start_span_config"),
	)
	defer s.Finish()
	v, _ := s.meta.Get("k2")
	assert.Equal("should_override", v)
	v, _ = s.meta.Get("key")
	assert.Equal("after_start_span_config", v)
}

func optsTestConsumer(opts ...StartSpanOption) {
	var cfg StartSpanConfig
	for _, o := range opts {
		o(&cfg)
	}
}

func BenchmarkConfig(b *testing.B) {
	// Don't use b.Loop() here because it'll cause measurement artifacts.
	b.Run("scenario_none", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			optsTestConsumer(
				ServiceName("SomeService"),
				ResourceName("SomeResource"),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
		}
	})
	b.Run("scenario_WithStartSpanConfig", func(b *testing.B) {
		b.ReportAllocs()
		cfg := NewStartSpanConfig(
			ServiceName("SomeService"),
			ResourceName("SomeResource"),
		)
		b.ResetTimer()
		for range b.N {
			optsTestConsumer(
				WithStartSpanConfig(cfg),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
		}
	})
}

// BenchmarkTagsVsWithTags mimics a typical contrib call site (e.g. valkey,
// mongo) that sets a handful of tags per span: some static across every call
// on a given client (Component, SpanKind, DBSystem, TargetHost, TargetPort)
// and one dynamic per-call value (ResourceName). It goes through a real
// tracer.StartSpan call, like the contrib code does through
// tracer.StartSpanFromContext, so option values escape to the heap the same
// way they do in production instead of being optimized away by inlining.
func BenchmarkTagsVsWithTags(b *testing.B) {
	b.Run("scenario_Tag_per_call", func(b *testing.B) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			s := tracer.StartSpan("test",
				Tag(ext.Component, "some-component"),
				Tag(ext.SpanKind, ext.SpanKindClient),
				Tag(ext.DBSystem, "some-db"),
				Tag(ext.TargetHost, "localhost"),
				Tag(ext.TargetPort, "1234"),
				Tag(ext.ResourceName, "some-resource"),
			)
			s.Finish()
		}
	})
	b.Run("scenario_WithTags_and_static_base", func(b *testing.B) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(b, err)
		base := NewStartSpanConfig(
			Tag(ext.Component, "some-component"),
			Tag(ext.SpanKind, ext.SpanKindClient),
			Tag(ext.DBSystem, "some-db"),
			Tag(ext.TargetHost, "localhost"),
			Tag(ext.TargetPort, "1234"),
		)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			s := tracer.StartSpan("test",
				WithTags(map[string]any{ext.ResourceName: "some-resource"}),
				WithStartSpanConfig(base),
			)
			s.Finish()
		}
	})
}

func BenchmarkStartSpanConfig(b *testing.B) {
	b.Run("scenario_none", func(b *testing.B) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			tracer.StartSpan("test",
				ServiceName("SomeService"),
				ResourceName("SomeResource"),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)

		}
	})
	b.Run("scenario_WithStartSpanConfig", func(b *testing.B) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(b, err)
		b.ReportAllocs()
		cfg := NewStartSpanConfig(
			ServiceName("SomeService"),
			ResourceName("SomeResource"),
		)
		b.ResetTimer()
		for range b.N {
			tracer.StartSpan("test",
				WithStartSpanConfig(cfg),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
		}
	})
}

func TestNoHTTPClientOverride(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		client := http.DefaultClient
		client.Timeout = 30 * time.Second // Default is 10s
		c, err := newTestConfig(
			WithHTTPClient(client),
			WithUDS("/tmp/agent.sock"),
		)
		assert.Nil(err)
		assert.Equal(30*time.Second, c.httpClient.Timeout)
	})
}

func TestCanComputeStats(t *testing.T) {
	t.Run("no-stats-endpoint", func(t *testing.T) {
		// When the agent does not support the /v0.6/stats endpoint,
		// client-side stats should not be computed
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":[],"client_drop_p0s":true}`))
		}))
		defer srv.Close()
		c, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithStatsComputation(true))
		assert.NoError(t, err)
		assert.False(t, c.canComputeStats())
		assert.False(t, c.canDropP0s())
	})

	t.Run("no-client-drop-p0s", func(t *testing.T) {
		// When the agent does not support client_drop_p0s,
		// client-side stats should not be computed
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":false}`))
		}))
		defer srv.Close()
		c, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithStatsComputation(true))
		assert.NoError(t, err)
		assert.False(t, c.canComputeStats())
		assert.False(t, c.canDropP0s())
	})

	t.Run("stats-disabled", func(t *testing.T) {
		// When stats computation is explicitly disabled,
		// client-side stats should not be computed even if agent supports it
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true}`))
		}))
		defer srv.Close()
		c, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithStatsComputation(false))
		assert.NoError(t, err)
		assert.False(t, c.canComputeStats())
		assert.False(t, c.canDropP0s())
	})

	t.Run("both-conditions-met", func(t *testing.T) {
		// When both conditions are met (stats endpoint + client_drop_p0s),
		// client-side stats should be computed
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true}`))
		}))
		defer srv.Close()
		c, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithStatsComputation(true))
		assert.NoError(t, err)
		assert.True(t, c.canComputeStats())
		assert.True(t, c.canDropP0s())
	})

	t.Run("discovery-feature-flag", func(t *testing.T) {
		// When discovery feature flag is enabled and agent supports both features,
		// client-side stats should be computed
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true}`))
		}))
		defer srv.Close()
		c, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithFeatureFlags("discovery"))
		assert.NoError(t, err)
		assert.True(t, c.canComputeStats())
		assert.True(t, c.canDropP0s())
	})

	t.Run("discovery-flag-missing-client-drop-p0s", func(t *testing.T) {
		// When discovery flag is enabled but client_drop_p0s is not supported,
		// client-side stats should not be computed
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":false}`))
		}))
		defer srv.Close()
		c, err := newTestConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithFeatureFlags("discovery"))
		assert.NoError(t, err)
		assert.False(t, c.canComputeStats())
		assert.False(t, c.canDropP0s())
	})
}

// Regression: agentless flag set without CI Visibility enabled must not disable the agent.
func TestAgentEnabledWithAgentlessEnvOnly(t *testing.T) {
	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	c, err := newTestConfig()
	require.NoError(t, err)
	assert.True(t, c.agentEnabled(), "agent must remain enabled when CI Visibility is off")
	assert.False(t, c.internalConfig.CIVisibilityAgentlessActive(), "agentless mode must not be active without CI Visibility")
}
