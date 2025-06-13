// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// We simulate the agent not being able to provide the statsd port
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
	require.NoError(t, err)
	testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8111"))
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
		cfg, err := newConfig(WithAgentTimeout(2))
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

		cfg, err := newConfig(WithAgentTimeout(2))
		assert.NoError(t, err)
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// We simulate the agent not being able to provide the statsd port
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
		assert.NoError(t, err)
		testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8111"))
	})

	t.Run("agent", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"endpoints": [], "config": {"statsd_port":0}}`))
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
			assert.NoError(t, err)
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8125"))
		})

		t.Run("port", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"endpoints": [], "config": {"statsd_port":8999}}`))
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			assert.NoError(t, err)
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8999"))
		})
	})
}

func TestLoadAgentFeatures(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		t.Run("disabled", func(t *testing.T) {
			cfg, err := newConfig(WithLambdaMode(true), WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})

		t.Run("unreachable", func(t *testing.T) {
			if testing.Short() {
				return
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr("127.9.9.9:8181"), WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})

		t.Run("StatusNotFound", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
			require.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})

		t.Run("error", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte("Not JSON"))
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
			require.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})
	})

	t.Run("OK", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"feature_flags":["a","b"],"client_drop_p0s":true,"obfuscation_version":2,"peer_tags":["peer.hostname"],"config": {"statsd_port":8999}}`))
		}))
		defer srv.Close()
		cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.NoError(t, err)
		assert.True(t, cfg.agent.DropP0s)
		assert.Equal(t, cfg.agent.StatsdPort, 8999)
		assert.EqualValues(t, cfg.agent.featureFlags, map[string]struct{}{
			"a": {},
			"b": {},
		})
		assert.True(t, cfg.agent.Stats)
		assert.True(t, cfg.agent.HasFlag("a"))
		assert.True(t, cfg.agent.HasFlag("b"))
		assert.EqualValues(t, cfg.agent.peerTags, []string{"peer.hostname"})
		assert.Equal(t, 2, cfg.agent.obfuscationVersion)
	})

	t.Run("discovery", func(t *testing.T) {
		t.Setenv("DD_TRACE_FEATURES", "discovery")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"config":{"statsd_port":8999}}`))
		}))
		defer srv.Close()
		cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
		assert.NoError(t, err)
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
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		cfg.loadContribIntegrations(nil)
		assert.Equal(t, 53, len(cfg.integrations))
		for integrationName, v := range cfg.integrations {
			assert.False(t, v.Instrumented, "integrationName=%s", integrationName)
		}
	}
	t.Run("default_before", defaultUninstrumentedTest)

	t.Run("OK import", func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		ok := MarkIntegrationImported("github.com/go-chi/chi")
		assert.True(t, ok)
		cfg.loadContribIntegrations([]*debug.Module{})
		assert.True(t, cfg.integrations["chi"].Instrumented)
	})

	t.Run("available", func(t *testing.T) {
		cfg, err := newConfig()
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
		cfg, err := newConfig()
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

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
	Imports    []string
}

func TestIntegrationEnabled(t *testing.T) {
	root, err := filepath.Abs("../../contrib")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, _ error) error {
		if filepath.Base(path) != "go.mod" || strings.Contains(path, fmt.Sprintf("%cinternal", os.PathSeparator)) {
			return nil
		}
		rErr := testIntegrationEnabled(t, filepath.Dir(path))
		if rErr != nil {
			return fmt.Errorf("path: %s, err: %w", path, rErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testIntegrationEnabled(t *testing.T, contribPath string) error {
	t.Helper()
	t.Log(contribPath)
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(pwd)
	}()
	if err = os.Chdir(contribPath); err != nil {
		return err
	}
	body, err := exec.Command("go", "list", "-json", "./...").Output()
	if err != nil {
		return fmt.Errorf("unable to get package info: %w", err)
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			return err
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") || strings.Contains(pkg.ImportPath, "/cmd") {
			continue
		}
		if hasInstrumentationImport(pkg) {
			return nil
		}
	}
	return fmt.Errorf(`package %q is expected use instrumentation telemetry. For more info see https://github.com/DataDog/dd-trace-go/blob/main/contrib/README.md#instrumentation-telemetry`, contribPath)
}

func hasInstrumentationImport(p contribPkg) bool {
	for _, imp := range p.Imports {
		if imp == "github.com/DataDog/dd-trace-go/v2/instrumentation" {
			return true
		}
	}
	return false
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
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(float64(1), c.sampler.Rate())
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
		c, err := newConfig(WithAgentTimeout(2))
		assert.NoError(t, err)
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
			tracer, err := newTracer(WithDebugMode(true))
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.True(t, c.debug)
		})
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG", "true")
			c, err := newConfig()
			assert.NoError(t, err)
			assert.True(t, c.debug)
		})
		t.Run("otel-env-debug", func(t *testing.T) {
			t.Setenv("OTEL_LOG_LEVEL", "debug")
			c, err := newConfig()
			assert.NoError(t, err)
			assert.True(t, c.debug)
		})
		t.Run("otel-env-notdebug", func(t *testing.T) {
			// any value other than debug, does nothing
			t.Setenv("OTEL_LOG_LEVEL", "notdebug")
			c, err := newConfig()
			assert.NoError(t, err)
			assert.False(t, c.debug)
		})
		t.Run("override-chain", func(t *testing.T) {
			assert := assert.New(t)
			// option override otel
			t.Setenv("OTEL_LOG_LEVEL", "debug")
			c, err := newConfig(WithDebugMode(false))
			assert.NoError(err)
			assert.False(c.debug)
			// env override otel
			t.Setenv("DD_TRACE_DEBUG", "false")
			c, err = newConfig()
			assert.NoError(err)
			assert.False(c.debug)
			// option override env
			c, err = newConfig(WithDebugMode(true))
			assert.NoError(err)
			assert.True(c.debug)
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
			assert.Equal(t, "localhost:8125", c.dogstatsdAddr)
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-agent_host", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "localhost")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:8125", c.dogstatsdAddr)
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-dogstatsd_host", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_HOST", "localhost")
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:8125", c.dogstatsdAddr)
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-port", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			tracer, err := newTracer(opts...)
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, "localhost:8125", c.dogstatsdAddr)
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("env-port: agent not available", func(t *testing.T) {
			t.Setenv("DD_DOGSTATSD_PORT", "123")
			fail = true
			tracer, err := newTracer(opts...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "localhost:123", c.dogstatsdAddr)
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
			assert.Equal(t, "localhost:8125", c.dogstatsdAddr)
			assert.Equal(t, "localhost:8125", globalconfig.DogstatsdAddr())
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
			assert.Equal(t, "localhost:123", c.dogstatsdAddr)
			assert.Equal(t, "localhost:123", globalconfig.DogstatsdAddr())
			fail = false
		})

		t.Run("option", func(t *testing.T) {
			o := make([]StartOption, len(opts))
			copy(o, opts)
			o = append(o, WithDogstatsdAddr("10.1.0.12:4002"))
			tracer, err := newTracer(o...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "10.1.0.12:8125", c.dogstatsdAddr)
			assert.Equal(t, "10.1.0.12:8125", globalconfig.DogstatsdAddr())
		})

		t.Run("option: agent not available", func(t *testing.T) {
			o := make([]StartOption, len(opts))
			copy(o, opts)
			fail = true
			o = append(o, WithDogstatsdAddr("10.1.0.12:4002"))
			tracer, err := newTracer(o...)
			assert.NoError(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, "10.1.0.12:4002", c.dogstatsdAddr)
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
			assert.Equal("unix://"+addr, c.dogstatsdAddr)
			assert.Equal("unix://"+addr, globalconfig.DogstatsdAddr())
		})
	})

	t.Run("env-env", func(t *testing.T) {
		t.Setenv("DD_ENV", "testEnv")
		tracer, err := newTracer(WithAgentTimeout(2))
		assert.NoError(t, err)
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(t, "testEnv", c.env)
	})

	t.Run("env-agentAddr", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "localhost")
		tracer, err := newTracer(WithAgentTimeout(2))
		assert.NoError(t, err)
		defer tracer.Stop()
		c := tracer.config
		assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:8126"}, c.agentURL)
	})

	t.Run("env-agentURL", func(t *testing.T) {
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "127.0.0.1:1234"}, c.agentURL)
		})

		t.Run("override-env", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "localhost")
			t.Setenv("DD_TRACE_AGENT_PORT", "3333")
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "127.0.0.1:1234"}, c.agentURL)
		})

		t.Run("code-override", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentAddr("localhost:3333"))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:3333"}, c.agentURL)
		})

		t.Run("code-override-full-URL", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://127.0.0.1:1234")
			tracer, err := newTracer(WithAgentURL("http://localhost:3333"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:3333"}, c.agentURL)
		})

		t.Run("code-full-UDS", func(t *testing.T) {
			tracer, err := newTracer(WithAgentURL("unix:///var/run/datadog/apm.socket"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "UDS__var_run_datadog_apm.socket"}, c.agentURL)
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
			assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:1234"}, c.agentURL)
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
		assert.Equal(env, c.env)
	})

	t.Run("trace_enabled", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.True(t, c.enabled.current)
			assert.Equal(t, c.enabled.cfgOrigin, telemetry.OriginDefault)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv("DD_TRACE_ENABLED", "false")
			tracer, err := newTracer(WithAgentTimeout(2))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.False(t, c.enabled.current)
			assert.Equal(t, c.enabled.cfgOrigin, telemetry.OriginEnvVar)
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
		assert.Equal(&url.URL{Scheme: "http", Host: "127.0.0.1:58126"}, c.agentURL)
		assert.NotNil(c.globalTags.get())
		assert.Equal("v", c.globalTags.get()["k"])
		assert.Equal("testEnv", c.env)
		assert.True(c.debug)
	})

	t.Run("env-tags", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env:test, aKey:aVal,bKey:bVal, cKey:")

		assert := assert.New(t)
		c, err := newConfig(WithAgentTimeout(2))
		assert.NoError(err)
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
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.True(t, c.profilerEndpoints)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv(traceprof.EndpointEnvVar, "false")
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.False(t, c.profilerEndpoints)
		})
	})

	t.Run("profiler-hotspots", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.True(t, c.profilerHotspots)
		})

		t.Run("override", func(t *testing.T) {
			t.Setenv(traceprof.CodeHotspotsEnvVar, "false")
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.False(t, c.profilerHotspots)
		})
	})

	t.Run("env-mapping", func(t *testing.T) {
		t.Setenv("DD_SERVICE_MAPPING", "tracer.test:test2, svc:Newsvc,http.router:myRouter, noval:")

		assert := assert.New(t)
		c, err := newConfig(WithAgentTimeout(2))

		assert.NoError(err)
		assert.Equal("test2", c.serviceMappings["tracer.test"])
		assert.Equal("Newsvc", c.serviceMappings["svc"])
		assert.Equal("myRouter", c.serviceMappings["http.router"])
		assert.Equal("", c.serviceMappings["noval"])
	})

	t.Run("datadog-tags", func(t *testing.T) {
		t.Run("can-set-value", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "200")
			assert := assert.New(t)
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(200, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("default", func(t *testing.T) {
			assert := assert.New(t)
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(128, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("clamped-to-zero", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "-520")
			assert := assert.New(t)
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(0, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("upper-clamp", func(t *testing.T) {
			t.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "1000")
			assert := assert.New(t)
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[0].(*propagator)
			assert.Equal(512, p.cfg.MaxTagsHeaderLen)
		})
	})

	t.Run("attribute-schema", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, 0, c.spanAttributeSchemaVersion)
			assert.Equal(t, false, namingschema.UseGlobalServiceName())
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, 1, c.spanAttributeSchemaVersion)
			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})

		t.Run("options", func(t *testing.T) {
			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			WithGlobalServiceName(true)(c)

			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})
	})

	t.Run("peer-service", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, c.peerServiceDefaultsEnabled, false)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("defaults-with-schema-v1", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", "true")
			t.Setenv("DD_TRACE_PEER_SERVICE_MAPPING", "old:new,old2:new2")
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})

		t.Run("options", func(t *testing.T) {
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			WithPeerServiceDefaults(true)(c)
			WithPeerServiceMapping("old", "new")(c)
			WithPeerServiceMapping("old2", "new2")(c)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})
	})

	t.Run("debug-open-spans", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, false, c.debugAbandonedSpans)
			assert.Equal(t, time.Duration(0), c.spanTimeout)
		})

		t.Run("debug-on", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, 10*time.Minute, c.spanTimeout)
		})

		t.Run("timeout-set", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			t.Setenv("DD_TRACE_ABANDONED_SPAN_TIMEOUT", fmt.Sprint(time.Minute))
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, time.Minute, c.spanTimeout)
		})

		t.Run("with-function", func(t *testing.T) {
			c, err := newConfig(WithAgentTimeout(2))
			assert.NoError(t, err)
			WithDebugSpansMode(time.Second)(c)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, time.Second, c.spanTimeout)
		})
	})

	t.Run("agent-timeout", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, time.Duration(10*time.Second), c.httpClient.Timeout)
		})
	})

	t.Run("trace-retries", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		assert.Equal(t, 0, c.sendRetries)
		assert.Equal(t, time.Millisecond, c.retryInterval)
	})
}

func TestTraceRetry(t *testing.T) {
	t.Run("sendRetries", func(t *testing.T) {
		c, err := newConfig(WithSendRetries(10))
		assert.NoError(t, err)
		assert.Equal(t, 10, c.sendRetries)
	})
	t.Run("retryInterval", func(t *testing.T) {
		c, err := newConfig(WithRetryInterval(10))
		assert.NoError(t, err)
		assert.Equal(t, 10*time.Second, c.retryInterval)
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
		y := *defaultHTTPClient(2)
		compareHTTPClients(t, x, y)
		assert.False(t, getFuncName(x.Transport.(*http.Transport).DialContext) == getFuncName(defaultDialer.DialContext))

	})
}

func TestDefaultDogstatsdAddr(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8125")
	})

	t.Run("host-env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_HOST", "111.111.1.1")
		t.Setenv("DD_AGENT_HOST", "222.222.2.2")
		assert.Equal(t, "111.111.1.1:8125", defaultDogstatsdAddr())
	})

	t.Run("port-env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
		t.Setenv("DD_AGENT_HOST", "222.222.2.2")
		assert.Equal(t, defaultDogstatsdAddr(), "222.222.2.2:8111")
	})

	t.Run("host-env+port-env", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_HOST", "111.111.1.1")
		t.Setenv("DD_DOGSTATSD_PORT", "8888")
		t.Setenv("DD_AGENT_HOST", "222.222.2.2")
		assert.Equal(t, "111.111.1.1:8888", defaultDogstatsdAddr())
	})

	t.Run("host-env+socket", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_HOST", "111.111.1.1")
		assert.Equal(t, "111.111.1.1:8125", defaultDogstatsdAddr())
		f, err := os.CreateTemp("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, "111.111.1.1:8125", defaultDogstatsdAddr())
	})

	t.Run("port-env+socket", func(t *testing.T) {
		t.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
		f, err := os.CreateTemp("", "dsd.socket")
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
		f, err := os.CreateTemp("", "dsd.socket")
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
	t.Run("WithService", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newConfig(
			WithService("api-intake"),
		)
		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("env", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		t.Setenv("DD_SERVICE", "api-intake")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("otel-env", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		t.Setenv("OTEL_SERVICE_NAME", "api-intake")
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newConfig(WithGlobalTag("service", "api-intake"))
		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=api-intake")
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		t.Setenv("DD_TAGS", "service:api-intake")
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)

		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("override-chain", func(t *testing.T) {
		defer func() {
			globalconfig.SetServiceName("")
		}()
		assert := assert.New(t)
		globalconfig.SetServiceName("")
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(c.serviceName, filepath.Base(os.Args[0]))
		assert.Equal("", globalconfig.ServiceName())

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=testService6")
		globalconfig.SetServiceName("")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService6")
		assert.Equal("testService6", globalconfig.ServiceName())

		t.Setenv("DD_TAGS", "service:testService")
		globalconfig.SetServiceName("")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService")
		assert.Equal("testService", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"))
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService2")
		assert.Equal("testService2", globalconfig.ServiceName())

		t.Setenv("OTEL_SERVICE_NAME", "testService3")
		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"))
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService3")
		assert.Equal("testService3", globalconfig.ServiceName())

		t.Setenv("DD_SERVICE", "testService4")
		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"), WithService("testService4"))
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService4")
		assert.Equal("testService4", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"), WithService("testService5"))
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService5")
		assert.Equal("testService5", globalconfig.ServiceName())
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
		c, err := newConfig()
		assert.NoError(err)
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
			c, err := newConfig()
			assert.NoError(err)
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
		c, err := newConfig(
			WithServiceVersion("1.2.3"),
		)
		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_VERSION", "1.2.3")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithGlobalTag("version", "1.2.3"))
		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.2.3")
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)

		assert.Equal("1.2.3", c.version)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "version:1.2.3")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(c.version, "")

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.1.0")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal("1.1.0", c.version)

		t.Setenv("DD_TAGS", "version:1.1.1")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal("1.1.1", c.version)

		c, err = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.NoError(err)
		assert.Equal("1.1.2", c.version)

		t.Setenv("DD_VERSION", "1.1.3")
		c, err = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.NoError(err)
		assert.Equal("1.1.3", c.version)

		c, err = newConfig(WithGlobalTag("version", "1.1.2"), WithServiceVersion("1.1.4"))
		assert.NoError(err)
		assert.Equal("1.1.4", c.version)
	})
}

func TestEnvConfig(t *testing.T) {
	t.Run("WithEnv", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(
			WithEnv("testing"),
		)
		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_ENV", "testing")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithGlobalTag("env", "testing"))
		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("OTEL_RESOURCE_ATTRIBUTES", func(t *testing.T) {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=testing")
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)

		assert.Equal("testing", c.env)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		t.Setenv("DD_TAGS", "env:testing")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(c.env, "")

		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=testing0")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal("testing0", c.env)

		t.Setenv("DD_TAGS", "env:testing1")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal("testing1", c.env)

		c, err = newConfig(WithGlobalTag("env", "testing2"))
		assert.NoError(err)
		assert.Equal("testing2", c.env)

		t.Setenv("DD_ENV", "testing3")
		c, err = newConfig(WithGlobalTag("env", "testing2"))
		assert.NoError(err)
		assert.Equal("testing3", c.env)

		c, err = newConfig(WithGlobalTag("env", "testing2"), WithEnv("testing4"))
		assert.NoError(err)
		assert.Equal("testing4", c.env)
	})
}

func TestStatsTags(t *testing.T) {
	assert := assert.New(t)
	c, err := newConfig(WithService("serviceName"), WithEnv("envName"))
	assert.NoError(err)
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
		c, err := newConfig(WithHostname("hostname"))
		assert.NoError(err)
		assert.Equal("hostname", c.hostname)
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal("hostname-env", c.hostname)
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)

		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		c, err := newConfig(WithHostname("hostname-middleware"))
		assert.NoError(err)
		assert.Equal("hostname-middleware", c.hostname)
	})
}

func TestWithTraceEnabled(t *testing.T) {
	t.Run("WithTraceEnabled", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithTraceEnabled(false))
		assert.NoError(err)
		assert.False(c.enabled.current)
	})

	t.Run("otel-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		c, err := newConfig()
		assert.NoError(err)
		assert.False(c.enabled.current)
	})

	t.Run("dd-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_ENABLED", "false")
		c, err := newConfig()
		assert.NoError(err)
		assert.False(c.enabled.current)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		// dd env overrides otel env
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		t.Setenv("DD_TRACE_ENABLED", "true")
		c, err := newConfig()
		assert.NoError(err)
		assert.True(c.enabled.current)
		// tracer option overrides dd env
		c, err = newConfig(WithTraceEnabled(false))
		assert.NoError(err)
		assert.False(c.enabled.current)
	})
}

func TestWithLogStartup(t *testing.T) {
	c, err := newConfig()
	assert.NoError(t, err)
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

	t.Run("envvar-invalid", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		t.Setenv("DD_TRACE_HEADER_TAGS", "header1:")

		assert := assert.New(t)
		newConfig()

		assert.Equal(0, globalconfig.HeaderTagsLen())
	})

	t.Run("envvar-partially-invalid", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		t.Setenv("DD_TRACE_HEADER_TAGS", "header1,header2:")

		assert := assert.New(t)
		newConfig()

		assert.Equal(1, globalconfig.HeaderTagsLen())
		fmt.Println(globalconfig.HeaderTagMap())
		assert.Equal(ext.HTTPRequestHeaders+".header1", globalconfig.HeaderTag("Header1"))
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
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.enableHostnameDetection)
	})
	t.Run("EnableViaEnv", func(t *testing.T) {
		t.Setenv("DD_TRACE_CLIENT_HOSTNAME_COMPAT", "v1.66")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.enableHostnameDetection)
	})
}

func TestPartialFlushing(t *testing.T) {
	t.Run("None", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Disabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "false")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Default-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, 10, c.partialFlushMinSpans)
	})
	t.Run("Enabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Enabled-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, 10, c.partialFlushMinSpans)
	})
	t.Run("Enabled-SetMinSpansNegative", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "-1")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("WithPartialFlushOption", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		WithPartialFlushing(20)(c)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, 20, c.partialFlushMinSpans)
	})
}

func TestWithStatsComputation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
	})
	t.Run("enabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithStatsComputation(true))
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
	})
	t.Run("disabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithStatsComputation(false))
		assert.NoError(err)
		assert.False(c.statsComputationEnabled)
	})
	t.Run("enabled-via-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "true")
		c, err := newConfig()
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
	})
	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "false")
		c, err := newConfig(WithStatsComputation(true))
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
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
	assert.Equal("value", s.meta["key"])
	assert.Equal(parent.Context().SpanID(), s.parentID)
	assert.Equal(parent.Context().TraceID(), s.Context().TraceID())
	assert.Equal("resource", s.resource)
	assert.Equal(service, s.service)
	assert.Equal(spanID, s.spanID)
	assert.Equal(ext.SpanTypeWeb, s.spanType)
	assert.Equal(tm.UnixNano(), s.start)
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
	assert.Equal("should_override", s.meta["k2"])
	assert.Equal("after_start_span_config", s.meta["key"])
}

func optsTestConsumer(opts ...StartSpanOption) {
	var cfg StartSpanConfig
	for _, o := range opts {
		o(&cfg)
	}
}

func BenchmarkConfig(b *testing.B) {
	b.Run("scenario_none", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
			optsTestConsumer(
				WithStartSpanConfig(cfg),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
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
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
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
		c, err := newConfig(
			WithHTTPClient(client),
			WithUDS("/tmp/agent.sock"),
		)
		assert.Nil(err)
		assert.Equal(30*time.Second, c.httpClient.Timeout)
	})
}
