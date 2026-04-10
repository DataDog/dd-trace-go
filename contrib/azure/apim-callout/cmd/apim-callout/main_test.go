// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/proxy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestServer starts the callout server on ephemeral ports and returns the
// base URL and a cleanup function. It initializes the environment and tracer
// exactly like the real main() does.
func startTestServer(t *testing.T) (calloutURL, healthURL string) {
	t.Helper()

	t.Setenv("DD_APPSEC_RULES", "../../../../../internal/appsec/testdata/user_rules.json")

	initializeEnvironment()

	calloutPort := freePort(t)
	healthPort := freePort(t)

	cfg := config{
		port:           fmt.Sprintf("%d", calloutPort),
		host:           "127.0.0.1",
		healthPort:     fmt.Sprintf("%d", healthPort),
		bodyLimit:      proxy.DefaultBodyParsingSizeLimit,
		requestTimeout: 30 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 2)
	go func() { errCh <- startHealthCheck(ctx, cfg) }()
	go func() { errCh <- startCalloutServer(ctx, cfg) }()

	// Cancel context and wait for both goroutines to fully return (including
	// deferred tracer.Stop) before the test ends. Without this, -count=N runs
	// race on tracer lifecycle and leak pinned WAF memory.
	t.Cleanup(func() {
		cancel()
		for range 2 {
			<-errCh
		}
	})

	calloutURL = fmt.Sprintf("http://127.0.0.1:%d", calloutPort)
	healthURL = fmt.Sprintf("http://127.0.0.1:%d", healthPort)

	// Wait for both servers to be ready
	waitForReady(t, calloutURL+"/", healthURL+"/")

	return calloutURL, healthURL
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func waitForReady(t *testing.T, calloutURL, healthURL string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	// Wait for health check port
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for callout port — POST with empty JSON to get a 400 (proves the server is listening)
	for time.Now().Before(deadline) {
		resp, err := client.Post(calloutURL, "application/json", bytes.NewReader([]byte("{}")))
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not become ready in time")
}

type calloutMessage struct {
	Addresses json.RawMessage `json:"addresses"`
	Gateway   string          `json:"gateway,omitempty"`
	RequestID string          `json:"request-id,omitempty"`
	Phase     string          `json:"phase,omitempty"`
}

type calloutResult struct {
	RequestID        string              `json:"request-id,omitempty"`
	PropagateHeaders map[string][]string `json:"propagate-headers,omitempty"`
	AllowedBodySize  *int                `json:"allowed-body-size,omitempty"`
	Block            *blockResult        `json:"block,omitempty"`
}

type blockResult struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Content string              `json:"content,omitempty"`
}

type testRequestAddresses struct {
	Method     string              `json:"method"`
	Scheme     string              `json:"scheme"`
	Authority  string              `json:"authority"`
	Path       string              `json:"path"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
	Headers    map[string][]string `json:"headers"`
	Body       string              `json:"body,omitempty"`
}

type testResponseAddresses struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       string              `json:"body,omitempty"`
}

func marshalAddresses(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func postJSON(t *testing.T, url string, body any) calloutResult {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer resp.Body.Close()

	var result calloutResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

func TestE2E(t *testing.T) {
	calloutURL, healthURL := startTestServer(t)

	t.Run("health-check", func(t *testing.T) {
		resp, err := http.Get(healthURL + "/")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		assert.Equal(t, "ok", body["status"])
	})

	t.Run("normal-request-continue", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "GET",
				Scheme:    "https",
				Authority: "api.example.com",
				Path:      "/users/123",
				Headers:   map[string][]string{"User-Agent": {"Mozilla/5.0"}},
			}),
		})

		assert.Nil(t, result.Block)
		assert.NotEmpty(t, result.RequestID)
	})

	t.Run("request-then-response-continue", func(t *testing.T) {
		reqResult := postJSON(t, calloutURL+"/", calloutMessage{
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "GET",
				Scheme:    "https",
				Authority: "api.example.com",
				Path:      "/users/123",
				Headers:   map[string][]string{"User-Agent": {"Mozilla/5.0"}},
			}),
		})
		require.Nil(t, reqResult.Block)
		require.NotEmpty(t, reqResult.RequestID)

		respResult := postJSON(t, calloutURL+"/", calloutMessage{
			RequestID: reqResult.RequestID,
			Addresses: marshalAddresses(t, testResponseAddresses{
				StatusCode: 200,
				Headers:    map[string][]string{"Content-Type": {"application/json"}},
			}),
		})
		assert.Nil(t, respResult.Block)
	})

	t.Run("unknown-request-id-fail-open", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			RequestID: "does-not-exist",
			Addresses: marshalAddresses(t, testResponseAddresses{
				StatusCode: 200,
				Headers:    map[string][]string{},
			}),
		})
		assert.Nil(t, result.Block)
	})

	t.Run("block-on-query-param", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "GET",
				Scheme:    "https",
				Authority: "api.example.com",
				Path:      "/search?q=match-request-query",
				Headers:   map[string][]string{"User-Agent": {"Mozilla/5.0"}},
			}),
		})

		require.NotNil(t, result.Block)
		assert.Equal(t, 418, result.Block.Status)
		assert.Contains(t, result.Block.Headers, "Content-Type")
		assert.NotEmpty(t, result.Block.Content)

		// Verify base64-encoded body is valid JSON
		body, err := base64.StdEncoding.DecodeString(result.Block.Content)
		require.NoError(t, err)
		var blockBody map[string]any
		require.NoError(t, json.Unmarshal(body, &blockBody))
		assert.Contains(t, blockBody, "errors")
	})

	t.Run("block-on-user-agent", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "GET",
				Scheme:    "https",
				Authority: "api.example.com",
				Path:      "/",
				Headers:   map[string][]string{"User-Agent": {"dd-test-scanner-log-block"}},
			}),
		})

		require.NotNil(t, result.Block)
		assert.Equal(t, 403, result.Block.Status)
	})

	t.Run("block-on-cookie", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "OPTIONS",
				Scheme:    "https",
				Authority: "api.example.com",
				Path:      "/",
				Headers:   map[string][]string{"Cookie": {"foo=jdfoSDGFkivRG_234"}},
			}),
		})

		require.NotNil(t, result.Block)
		assert.Equal(t, 418, result.Block.Status)
	})

	t.Run("block-on-client-ip", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:     "GET",
				Scheme:     "https",
				Authority:  "api.example.com",
				Path:       "/",
				RemoteAddr: "111.222.111.222",
				Headers:    map[string][]string{"User-Agent": {"Mistake not..."}, "X-Forwarded-For": {"111.222.111.222"}},
			}),
		})

		require.NotNil(t, result.Block)
		assert.Equal(t, 403, result.Block.Status)
	})

	t.Run("invalid-json-fail-open", func(t *testing.T) {
		resp, err := http.Post(calloutURL+"/", "application/json", bytes.NewReader([]byte("not json")))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var result calloutResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Nil(t, result.Block)
	})

	t.Run("get-method-not-allowed", func(t *testing.T) {
		resp, err := http.Get(calloutURL + "/")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	// Direct protocol validation — exercises the calloutMessage/calloutResult
	// wire format against a real running server, including request-id lifecycle,
	// fail-open on unknown IDs, and bad JSON handling.

	t.Run("protocol-phase1-returns-request-id", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			Phase: "<RequestHeaders>",
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "GET",
				Scheme:    "https",
				Authority: "example.com",
				Path:      "/hello",
				Headers:   map[string][]string{"User-Agent": {"curl/8.0"}},
			}),
		})
		assert.Nil(t, result.Block)
		assert.NotEmpty(t, result.RequestID, "Phase 1 must return a request-id")
	})

	t.Run("protocol-phase3-continue-with-request-id", func(t *testing.T) {
		// Phase 1
		phase1 := postJSON(t, calloutURL+"/", calloutMessage{
			Phase: "<RequestHeaders>",
			Addresses: marshalAddresses(t, testRequestAddresses{
				Method:    "GET",
				Scheme:    "https",
				Authority: "example.com",
				Path:      "/hello",
				Headers:   map[string][]string{"User-Agent": {"curl/8.0"}},
			}),
		})
		require.NotEmpty(t, phase1.RequestID)

		// Phase 3: response headers with valid request-id
		phase3 := postJSON(t, calloutURL+"/", calloutMessage{
			RequestID: phase1.RequestID,
			Phase:     "<ResponseHeaders>",
			Addresses: marshalAddresses(t, testResponseAddresses{
				StatusCode: 200,
				Headers:    map[string][]string{"Content-Type": {"application/json"}},
			}),
		})
		assert.Nil(t, phase3.Block, "Phase 3 with valid request-id should not block")
		assert.Empty(t, phase3.RequestID, "Phase 3 response must not contain request-id")
	})

	t.Run("protocol-unknown-request-id-fail-open", func(t *testing.T) {
		result := postJSON(t, calloutURL+"/", calloutMessage{
			RequestID: "unknown-id-12345",
			Phase:     "<ResponseHeaders>",
			Addresses: marshalAddresses(t, testResponseAddresses{
				StatusCode: 200,
				Headers:    map[string][]string{},
			}),
		})
		assert.Nil(t, result.Block, "unknown request-id must fail-open")
	})

	t.Run("protocol-bad-json-returns-400", func(t *testing.T) {
		resp, err := http.Post(calloutURL+"/", "application/json", bytes.NewReader([]byte("not-json")))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var result calloutResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Nil(t, result.Block)
	})

	t.Run("protocol-health-check", func(t *testing.T) {
		resp, err := http.Get(healthURL + "/")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var body map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		assert.Equal(t, "ok", body["status"])
	})
}

// Tests ported from the HAProxy SPOA main_test.go pattern.

func TestInitializeEnvironment(t *testing.T) {
	cases := []struct {
		name   string
		preEnv map[string]string
		want   map[string]string
	}{
		{
			name: "defaults",
			want: nil, // will check against getDefaultEnvVars()
		},
		{
			name: "existing preserved",
			preEnv: map[string]string{
				"DD_APM_TRACING_ENABLED":     "true",
				"DD_APPSEC_WAF_TIMEOUT":      "5ms",
				"DD_TRACE_PROPAGATION_STYLE": "datadog,tracecontext,baggage",
			},
			want: map[string]string{
				"DD_APM_TRACING_ENABLED":     "true",
				"DD_APPSEC_WAF_TIMEOUT":      "5ms",
				"DD_TRACE_PROPAGATION_STYLE": "datadog,tracecontext,baggage",
			},
		},
	}

	var allKeys []string
	for k := range getDefaultEnvVars() {
		allKeys = append(allKeys, k)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(allKeys...)
			setEnv(tc.preEnv)

			initializeEnvironment()

			expected := tc.want
			if expected == nil {
				expected = getDefaultEnvVars()
			}
			for k, want := range expected {
				assert.Equal(t, want, os.Getenv(k), "%s should match", k)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	type want struct {
		port       string
		healthPort string
		host       string
		bodyLimit  int
	}

	cases := []struct {
		name string
		env  map[string]string
		want want
	}{
		{
			name: "defaults",
			want: want{"8080", "8081", "0.0.0.0", proxy.DefaultBodyParsingSizeLimit},
		},
		{
			name: "valid overrides",
			env: map[string]string{
				"DD_APIM_CALLOUT_PORT":              "1234",
				"DD_APIM_CALLOUT_HEALTHCHECK_PORT":  "4321",
				"DD_APIM_CALLOUT_HOST":              "127.0.0.1",
				"DD_APPSEC_BODY_PARSING_SIZE_LIMIT": "100000000",
			},
			want: want{"1234", "4321", "127.0.0.1", 100000000},
		},
		{
			name: "bad values fall back",
			env: map[string]string{
				"DD_APIM_CALLOUT_PORT":              "badport",
				"DD_APIM_CALLOUT_HEALTHCHECK_PORT":  "gopher",
				"DD_APPSEC_BODY_PARSING_SIZE_LIMIT": "notanint",
				"DD_APIM_CALLOUT_HOST":              "notanip",
			},
			want: want{"8080", "8081", "0.0.0.0", proxy.DefaultBodyParsingSizeLimit},
		},
	}

	allKeys := []string{
		"DD_APIM_CALLOUT_PORT",
		"DD_APIM_CALLOUT_HEALTHCHECK_PORT",
		"DD_APIM_CALLOUT_HOST",
		"DD_APPSEC_BODY_PARSING_SIZE_LIMIT",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(allKeys...)
			setEnv(tc.env)

			cfg := loadConfig()
			assert.Equal(t, tc.want.port, cfg.port, "port")
			assert.Equal(t, tc.want.healthPort, cfg.healthPort, "healthPort")
			assert.Equal(t, tc.want.host, cfg.host, "host")
			assert.Equal(t, tc.want.bodyLimit, cfg.bodyLimit, "bodyLimit")
		})
	}
}

func unsetEnv(keys ...string) {
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
}

func setEnv(env map[string]string) {
	for k, v := range env {
		_ = os.Setenv(k, v)
	}
}
