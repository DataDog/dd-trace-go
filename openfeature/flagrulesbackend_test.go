// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// createTestFlagsConfig creates a valid universalFlagsConfiguration for testing.
func createTestFlagsConfig() *universalFlagsConfiguration {
	return &universalFlagsConfiguration{
		CreatedAt: time.Now(),
		Format:    "SERVER",
		Environment: environment{
			Name: "test",
		},
		Flags: map[string]*flag{
			"test-flag": {
				Key:           "test-flag",
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on":  {Key: "on", Value: true},
					"off": {Key: "off", Value: false},
				},
				Allocations: []*allocation{
					{
						Key:   "allocation1",
						Rules: []*rule{},
						Splits: []*split{
							{
								Shards: []*shard{
									{
										Salt: "test",
										Ranges: []*shardRange{
											{Start: 0, End: 8192},
										},
										TotalShards: 8192,
									},
								},
								VariationKey: "on",
							},
						},
					},
				},
			},
		},
	}
}

func TestNewFlagRulesBackend(t *testing.T) {
	t.Run("requires URL", func(t *testing.T) {
		provider := newDatadogProvider()
		_, err := newFlagRulesBackend(FlagRulesConfig{}, provider)
		require.Error(t, err)
		require.Contains(t, err.Error(), "flag rules URL is required")
	})

	t.Run("accepts URL from config", func(t *testing.T) {
		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL: "https://example.com/flags.json",
		}, provider)
		require.NoError(t, err)
		require.NotNil(t, backend)
		require.Equal(t, "https://example.com/flags.json", backend.config.URL)
	})

	t.Run("uses default poll interval", func(t *testing.T) {
		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL: "https://example.com/flags.json",
		}, provider)
		require.NoError(t, err)
		require.Equal(t, defaultFlagRulesPollInterval, backend.config.PollInterval)
	})

	t.Run("accepts custom poll interval", func(t *testing.T) {
		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:          "https://example.com/flags.json",
			PollInterval: 60 * time.Second,
		}, provider)
		require.NoError(t, err)
		require.Equal(t, 60*time.Second, backend.config.PollInterval)
	})

	t.Run("accepts custom HTTP client", func(t *testing.T) {
		provider := newDatadogProvider()
		customClient := &http.Client{Timeout: 5 * time.Second}
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:        "https://example.com/flags.json",
			HTTPClient: customClient,
		}, provider)
		require.NoError(t, err)
		require.Equal(t, customClient, backend.client)
	})

	t.Run("creates default HTTP client when none provided", func(t *testing.T) {
		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL: "https://example.com/flags.json",
		}, provider)
		require.NoError(t, err)
		require.NotNil(t, backend.client)
	})
}

func TestFlagRulesBackend_FetchConfiguration(t *testing.T) {
	t.Run("fetches and applies valid configuration", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, err := json.Marshal(config)
		require.NoError(t, err)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		backend.fetchConfiguration()

		cfg := provider.getConfiguration()
		require.NotNil(t, cfg)
		require.Equal(t, "SERVER", cfg.Format)
		require.Len(t, cfg.Flags, 1)
		require.Contains(t, cfg.Flags, "test-flag")
	})

	t.Run("stores ETag for conditional requests", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"test-etag-123"`)
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		backend.fetchConfiguration()

		require.Equal(t, `"test-etag-123"`, backend.lastETag)
	})

	t.Run("stores Last-Modified for conditional requests", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		lastModified := "Wed, 11 Dec 2025 12:00:00 GMT"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Last-Modified", lastModified)
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		backend.fetchConfiguration()

		require.Equal(t, lastModified, backend.lastModified)
	})

	t.Run("sends conditional request headers", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		var receivedETag string
		requestCount := atomic.Int32{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := requestCount.Add(1)
			receivedETag = r.Header.Get("If-None-Match")

			if count > 1 && receivedETag == `"test-etag"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"test-etag"`)
			w.Header().Set("Last-Modified", "Wed, 11 Dec 2025 12:00:00 GMT")
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		// First fetch - no conditional headers
		backend.fetchConfiguration()
		require.Equal(t, int32(1), requestCount.Load())
		require.Empty(t, receivedETag)

		// Second fetch - should send conditional headers
		backend.fetchConfiguration()
		require.Equal(t, int32(2), requestCount.Load())
		require.Equal(t, `"test-etag"`, receivedETag)
	})

	t.Run("handles 304 Not Modified", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		requestCount := atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := requestCount.Add(1)

			if count == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("ETag", `"test-etag"`)
				w.WriteHeader(http.StatusOK)
				w.Write(configJSON)
				return
			}

			// Return 304 for subsequent requests
			w.WriteHeader(http.StatusNotModified)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		// First fetch
		backend.fetchConfiguration()
		cfg := provider.getConfiguration()
		require.NotNil(t, cfg)

		// Second fetch - 304, should keep existing config
		backend.fetchConfiguration()
		cfg2 := provider.getConfiguration()
		require.NotNil(t, cfg2)
		require.Equal(t, cfg, cfg2)
	})

	t.Run("handles HTTP error status codes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		backend.fetchConfiguration()

		// Configuration should remain nil (not updated)
		require.Nil(t, provider.getConfiguration())
	})

	t.Run("handles invalid JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{invalid json"))
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		backend.fetchConfiguration()

		// Configuration should remain nil (not updated)
		require.Nil(t, provider.getConfiguration())
	})

	t.Run("handles invalid configuration format", func(t *testing.T) {
		invalidConfig := &universalFlagsConfiguration{
			Format: "INVALID",
			Flags:  map[string]*flag{},
		}
		configJSON, _ := json.Marshal(invalidConfig)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		backend.fetchConfiguration()

		// Configuration should remain nil (validation failed)
		require.Nil(t, provider.getConfiguration())
	})

	t.Run("preserves existing config on fetch failure", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		requestCount := atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count := requestCount.Add(1)

			if count == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write(configJSON)
				return
			}

			// Fail subsequent requests
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{URL: server.URL}, provider)
		require.NoError(t, err)

		// First fetch succeeds
		backend.fetchConfiguration()
		cfg := provider.getConfiguration()
		require.NotNil(t, cfg)

		// Second fetch fails - config should be preserved
		backend.fetchConfiguration()
		cfg2 := provider.getConfiguration()
		require.NotNil(t, cfg2)
		require.Equal(t, cfg, cfg2)
	})
}

func TestFlagRulesBackend_PollingLoop(t *testing.T) {
	t.Run("polls at configured interval", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		requestCount := atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:          server.URL,
			PollInterval: 50 * time.Millisecond,
		}, provider)
		require.NoError(t, err)

		backend.Start()
		defer backend.Stop()

		// Wait for a few poll cycles
		time.Sleep(200 * time.Millisecond)

		// Should have made multiple requests (initial + polling)
		count := requestCount.Load()
		require.GreaterOrEqual(t, count, int32(3), "expected at least 3 requests, got %d", count)
	})

	t.Run("initial fetch happens immediately", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		requestCount := atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:          server.URL,
			PollInterval: 10 * time.Second, // Long interval
		}, provider)
		require.NoError(t, err)

		backend.Start()
		defer backend.Stop()

		// Give a small amount of time for the initial fetch
		time.Sleep(50 * time.Millisecond)

		// Should have made at least 1 request (the initial fetch)
		require.GreaterOrEqual(t, requestCount.Load(), int32(1))

		// Provider should have configuration
		require.NotNil(t, provider.getConfiguration())
	})
}

func TestFlagRulesBackend_Stop(t *testing.T) {
	t.Run("stop is idempotent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"format":"SERVER","flags":{}}`))
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:          server.URL,
			PollInterval: 10 * time.Millisecond,
		}, provider)
		require.NoError(t, err)

		backend.Start()

		// Stop multiple times should not panic
		require.NoError(t, backend.Stop())
		require.NoError(t, backend.Stop())
		require.NoError(t, backend.Stop())
	})

	t.Run("stop returns quickly", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"format":"SERVER","flags":{}}`))
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:          server.URL,
			PollInterval: 10 * time.Millisecond,
		}, provider)
		require.NoError(t, err)

		backend.Start()

		done := make(chan struct{})
		go func() {
			backend.Stop()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(time.Second):
			t.Fatal("Stop() did not return in time")
		}
	})

	t.Run("polling stops after Stop", func(t *testing.T) {
		config := createTestFlagsConfig()
		configJSON, _ := json.Marshal(config)

		requestCount := atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(configJSON)
		}))
		defer server.Close()

		provider := newDatadogProvider()
		backend, err := newFlagRulesBackend(FlagRulesConfig{
			URL:          server.URL,
			PollInterval: 20 * time.Millisecond,
		}, provider)
		require.NoError(t, err)

		backend.Start()

		// Wait for some requests
		time.Sleep(100 * time.Millisecond)
		countBeforeStop := requestCount.Load()

		backend.Stop()

		// Wait a bit more
		time.Sleep(100 * time.Millisecond)
		countAfterStop := requestCount.Load()

		// Count should not have increased significantly after stop
		// Allow for one more request that might have been in flight
		require.LessOrEqual(t, countAfterStop-countBeforeStop, int32(1),
			"polling should have stopped, but requests continued")
	})
}

func TestFlagRulesBackend_ConcurrentAccess(t *testing.T) {
	config := createTestFlagsConfig()
	configJSON, _ := json.Marshal(config)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
		w.Write(configJSON)
	}))
	defer server.Close()

	provider := newDatadogProvider()
	backend, err := newFlagRulesBackend(FlagRulesConfig{
		URL:          server.URL,
		PollInterval: 10 * time.Millisecond,
	}, provider)
	require.NoError(t, err)

	backend.Start()
	defer backend.Stop()

	// Concurrent reads/writes should not cause data races
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				backend.fetchConfiguration()
			}
			done <- struct{}{}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
