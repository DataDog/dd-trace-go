// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package agent_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/agent"
)

func TestHTTPClient(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		server := httptest.NewServer(http.DefaultServeMux)
		defer server.Close()
		client := agent.HTTPClient()
		r, err := client.Head(server.URL)
		if err != nil {
			t.Error(err)
		}
		r.Body.Close()
	})

	t.Run("UDS", func(t *testing.T) {
		d := t.TempDir()
		udspath := path.Join(d, "agent.sock")
		ln, err := net.Listen("unix", udspath)
		if err != nil {
			t.Fatal(err)
		}
		var server http.Server
		go server.Serve(ln)
		defer server.Close()

		t.Setenv("DD_TRACE_AGENT_URL", fmt.Sprintf("unix://%s", udspath))
		client := agent.HTTPClient()
		// TODO: can we be sure the server is actually *serving* here?
		// We started it in a separate goroutine so there's no guarantee
		// that between then and now that the goroutine was ever
		// scheduled.
		r, err := client.Head("http://foobar/info")
		if err != nil {
			t.Error(err)
		}
		r.Body.Close()
	})
}

func TestResolveAgentAddr(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		unsetenv(t, "DD_AGENT_HOST")
		unsetenv(t, "DD_TRACE_AGENT_PORT")
		addr := agent.ResolveAgentAddr()
		assert.Equal(t, "localhost:8126", addr)
	})

	t.Run("host-port-env", func(t *testing.T) {
		t.Setenv("DD_AGENT_HOST", "foobar")
		t.Setenv("DD_TRACE_AGENT_PORT", "9999")
		addr := agent.ResolveAgentAddr()
		assert.Equal(t, "foobar:9999", addr)
	})

	t.Run("url-env", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "http://datadogagent.example.com")
		addr := agent.ResolveAgentAddr()
		assert.Equal(t, "datadogagent.example.com", addr)
	})

	t.Run("uds", func(t *testing.T) {
		unsetenv(t, "DD_AGENT_HOST")
		unsetenv(t, "DD_TRACE_AGENT_PORT")
		t.Setenv("DD_TRACE_AGENT_URL", "/var/file/socket.sock")
		addr := agent.ResolveAgentAddr()
		// When the connecting via a Unix domain socket, the address
		// doesn't actually matter
		assert.Equal(t, "localhost:8126", addr)
	})
}

func TestFeatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			return
		}
		w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"statsd_port":8999}`))
	}))
	defer server.Close()

	features, err := agent.LoadFeatures(server.Listener.Addr().String(), new(http.Client))
	if err != nil {
		t.Fatal(err)
	}
	expected := &agent.Features{
		Endpoints:     []string{"/v0.6/stats"},
		ClientDropP0s: true,
		StatsdPort:    8999,
	}
	assert.Equal(t, expected, features)
}

func unsetenv(t *testing.T, key string) {
	t.Helper()
	if old, ok := os.LookupEnv(key); ok {
		os.Unsetenv(key)
		t.Cleanup(func() {
			os.Setenv(key, old)
		})
	}
}
