// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/require"
)

// The RC client relies on Repository (in the datadog-agent) which performs config signature validation
// using some signing keys which we don't want to expose in this repository, making testing delicate.
// Testing is performed in the datadog agent for the components being used here, see https://github.com/DataDog/datadog-agent/tree/main/pkg/remoteconfig/state.
// Signature verification will be changed and made optional in the future, at which point integration testing will become possible
// as we will be able to setup a Repository and test applying updates, creating a client, etc... all of which require a valid
// Repository object at the moment

func TestRCClient(t *testing.T) {
	cfg := DefaultClientConfig()
	cfg.ServiceName = "test"
	var err error
	client, err = newClient(cfg)
	require.NoError(t, err)

	t.Run("registerCallback", func(t *testing.T) {
		client.callbacks = []Callback{}
		nilCallback := func(map[string]ProductUpdate) map[string]rc.ApplyStatus { return nil }
		defer func() { client.callbacks = []Callback{} }()
		require.Equal(t, 0, len(client.callbacks))
		err = RegisterCallback(nilCallback)
		require.NoError(t, err)
		require.Equal(t, 1, len(client.callbacks))
		require.Equal(t, 1, len(client.callbacks))
		err = RegisterCallback(nilCallback)
		require.NoError(t, err)
		require.Equal(t, 2, len(client.callbacks))
	})

	t.Run("apply-update", func(t *testing.T) {
		client.callbacks = []Callback{}
		cfgPath := "datadog/2/ASM_FEATURES/asm_features_activation/config"
		err = RegisterProduct(rc.ProductASMFeatures)
		require.NoError(t, err)
		err = RegisterCallback(func(updates map[string]ProductUpdate) map[string]rc.ApplyStatus {
			statuses := map[string]rc.ApplyStatus{}
			for p, u := range updates {
				if p == rc.ProductASMFeatures {
					require.NotNil(t, u)
					require.NotNil(t, u[cfgPath])
					require.Equal(t, string(u[cfgPath]), "test")
					statuses[cfgPath] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
				}
			}
			return statuses
		})
		require.NoError(t, err)

		resp := genUpdateResponse([]byte("test"), cfgPath)
		err := client.applyUpdate(resp)
		require.NoError(t, err)
	})

	t.Run("subscribe", func(t *testing.T) {
		client, err = newClient(cfg)
		require.NoError(t, err)

		cfgPath := "datadog/2/APM_TRACING/foo/bar"
		err = Subscribe(rc.ProductAPMTracing, func(u ProductUpdate) map[string]rc.ApplyStatus {
			statuses := map[string]rc.ApplyStatus{}
			require.NotNil(t, u)
			require.Len(t, u, 1)
			require.NotNil(t, u[cfgPath])
			require.Equal(t, string(u[cfgPath]), "test")
			statuses[cfgPath] = rc.ApplyStatus{State: rc.ApplyStateAcknowledged}
			return statuses
		})
		require.NoError(t, err)

		resp := genUpdateResponse([]byte("test"), cfgPath)
		err := client.applyUpdate(resp)
		require.NoError(t, err)
	})
}

func TestPayloads(t *testing.T) {
	t.Run("getConfigResponse", func(t *testing.T) {

		for _, tc := range []struct {
			name    string
			payload string
			cfg     clientGetConfigsResponse
		}{
			{
				name:    "empty",
				payload: "{}",
				cfg:     clientGetConfigsResponse{},
			},
			{
				name: "1-product",
				payload: `{
				"roots": ["dGVzdA=="],
				"targets": "dGVzdA==",
				"target_files":
				[
					{
						"path": "/path/to/ASM_FEATURES/config",
						"raw": "dGVzdA=="
					}
				],
				"client_configs":
				[
					"ASM_FEATURES"
				]
			}`,
				cfg: clientGetConfigsResponse{
					Roots:   [][]byte{[]byte("test")},
					Targets: []byte("test"),
					TargetFiles: []*file{
						{
							Path: "/path/to/ASM_FEATURES/config",
							Raw:  []byte("test"),
						},
					},
					ClientConfigs: []string{"ASM_FEATURES"},
				},
			},
			{
				name: "2-products",
				payload: `{
				"roots": ["dGVzdA==", "dGVzdA==", "dGVzdA=="],
				"targets": "dGVzdA==",
				"target_files":
				[
					{
						"path": "/path/to/ASM_FEATURES/config",
						"raw": "dGVzdA=="
					},
					{
						"path": "/path/to/ASM_DATA/config",
						"raw": "dGVzdA=="
					}
				],
				"client_configs":
				[
					"ASM_FEATURES",
					"ASM_DATA"
				]
			}`,
				cfg: clientGetConfigsResponse{
					Roots:   [][]byte{[]byte("test"), []byte("test"), []byte("test")},
					Targets: []byte("test"),
					TargetFiles: []*file{
						{
							Path: "/path/to/ASM_FEATURES/config",
							Raw:  []byte("test"),
						},
						{
							Path: "/path/to/ASM_DATA/config",
							Raw:  []byte("test"),
						},
					},
					ClientConfigs: []string{"ASM_FEATURES", "ASM_DATA"},
				},
			},
		} {
			cfg := tc.cfg
			payloadStr := tc.payload
			for _, str := range []string{"\t", "\n", " "} {
				payloadStr = strings.ReplaceAll(payloadStr, str, "")
			}
			payload := []byte(payloadStr)

			t.Run("marshall-"+tc.name, func(t *testing.T) {
				out, err := json.Marshal(cfg)
				require.NoError(t, err)
				require.Equal(t, payload, out)
			})

			t.Run("unmarshall-"+tc.name, func(t *testing.T) {
				var out clientGetConfigsResponse
				err := json.Unmarshal([]byte(payload), &out)
				require.NoError(t, err)
				require.Equal(t, cfg, out)

			})
		}
	})
}

func genUpdateResponse(payload []byte, cfgPath string) *clientGetConfigsResponse {
	var targets string
	targetsFmt := `{"signed":{"_type":"targets","custom":{"agent_refresh_interval":0,"opaque_backend_state":"test"},"expires":"2023-01-12T08:46:28Z","spec_version":"1.0.0","targets":{"%s":{"custom":{"c":["HX4ZhCZRs74V1_XaalnCY"],"tracer-predicates":{"tracer_predicates_v1":[{"clientID":"HX4ZhCZRs74V1_XaalnCY"}]},"v":87},"hashes":{"sha256":"%x"},"length":%d}},"version":33431626}}`
	sum := sha256.Sum256(payload)
	targets = fmt.Sprintf(targetsFmt, cfgPath, sum, len(payload))

	return &clientGetConfigsResponse{
		Targets:       []byte(targets),
		TargetFiles:   []*file{{Path: cfgPath, Raw: payload}},
		ClientConfigs: []string{cfgPath},
	}
}

func TestConfig(t *testing.T) {
	t.Run("poll-interval", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			env      string
			expected time.Duration
		}{
			{
				name:     "default",
				expected: 5 * time.Second,
			},
			{
				name:     "1s",
				env:      "1",
				expected: 1 * time.Second,
			},
			{
				name:     "1min",
				env:      "60",
				expected: 60 * time.Second,
			},
			{
				name:     "-1s",
				env:      "-1",
				expected: 5 * time.Second,
			},
			{
				name:     "invalid-1",
				env:      "10s",
				expected: 5 * time.Second,
			},
			{
				name:     "invalid-2",
				env:      "1b2",
				expected: 5 * time.Second,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv(envPollIntervalSec, tc.env)
				duration := pollIntervalFromEnv()
				require.Equal(t, tc.expected, duration)

			})
		}
	})
}

func dummyCallback1(map[string]ProductUpdate) map[string]rc.ApplyStatus {
	return nil
}
func dummyCallback2(map[string]ProductUpdate) map[string]rc.ApplyStatus {
	return map[string]rc.ApplyStatus{}
}

func dummyCallback3(map[string]ProductUpdate) map[string]rc.ApplyStatus {
	return map[string]rc.ApplyStatus{}
}

func dummyCallback4(map[string]ProductUpdate) map[string]rc.ApplyStatus {
	return map[string]rc.ApplyStatus{}
}

func TestRegistration(t *testing.T) {
	t.Run("callbacks", func(t *testing.T) {
		var err error
		client, err = newClient(DefaultClientConfig())
		require.NoError(t, err)

		err = RegisterCallback(dummyCallback1)
		require.NoError(t, err)
		require.Len(t, client.callbacks, 1)
		err = UnregisterCallback(dummyCallback1)
		require.NoError(t, err)
		require.Empty(t, client.callbacks)

		err = RegisterCallback(dummyCallback2)
		require.NoError(t, err)
		err = RegisterCallback(dummyCallback3)
		require.NoError(t, err)
		err = RegisterCallback(dummyCallback1)
		require.NoError(t, err)
		err = RegisterCallback(dummyCallback4)
		require.NoError(t, err)
		require.Len(t, client.callbacks, 4)

		err = UnregisterCallback(dummyCallback1)
		require.NoError(t, err)
		require.Len(t, client.callbacks, 3)
		for _, c := range client.callbacks {
			require.NotEqual(t, reflect.ValueOf(dummyCallback1), reflect.ValueOf(c))
		}

		err = UnregisterCallback(dummyCallback3)
		require.NoError(t, err)
		require.Len(t, client.callbacks, 2)
		for _, c := range client.callbacks {
			require.NotEqual(t, reflect.ValueOf(dummyCallback3), reflect.ValueOf(c))
		}
	})
}

func TestSubscribe(t *testing.T) {
	var err error
	client, err = newClient(DefaultClientConfig())
	require.NoError(t, err)

	var callback Callback = func(updates map[string]ProductUpdate) map[string]rc.ApplyStatus { return nil }
	var pCallback ProductCallback = func(u ProductUpdate) map[string]rc.ApplyStatus { return nil }

	err = Subscribe("my-product", pCallback)
	require.NoError(t, err)
	require.Len(t, client.callbacks, 0)
	require.Len(t, client.productsWithCallbacks, 1)
	require.Equal(t, reflect.ValueOf(pCallback), reflect.ValueOf(client.productsWithCallbacks["my-product"]))

	err = RegisterProduct("my-product")
	require.Error(t, err)
	require.Len(t, client.productsWithCallbacks, 1)

	err = RegisterProduct("my-second-product")
	require.NoError(t, err)
	require.Len(t, client.productsWithCallbacks, 1)

	err = Subscribe("my-second-product", pCallback)
	require.Error(t, err)
	require.Len(t, client.productsWithCallbacks, 1)

	err = RegisterCallback(callback)
	require.NoError(t, err)
	require.Len(t, client.callbacks, 1)
	require.Len(t, client.productsWithCallbacks, 1)
	require.Equal(t, reflect.ValueOf(callback), reflect.ValueOf(client.callbacks[0]))
}

func TestNewUpdateRequest(t *testing.T) {
	cfg := DefaultClientConfig()
	cfg.ServiceName = "test-svc"
	cfg.Env = "test-env"
	cfg.TracerVersion = "tracer-version"
	cfg.AppVersion = "app-version"
	var err error
	client, err = newClient(cfg)
	require.NoError(t, err)

	err = RegisterProduct("my-product")
	require.NoError(t, err)
	err = RegisterCapability(ASMActivation)
	require.NoError(t, err)
	err = Subscribe("my-second-product", func(u ProductUpdate) map[string]rc.ApplyStatus { return nil }, APMTracingSampleRate)
	require.NoError(t, err)

	b, err := client.newUpdateRequest()
	require.NoError(t, err)

	var req clientGetConfigsRequest
	err = json.Unmarshal(b.Bytes(), &req)
	require.NoError(t, err)

	require.Equal(t, []string{"my-product", "my-second-product"}, req.Client.Products)
	require.Equal(t, []uint8([]byte{0x10, 0x2}), req.Client.Capabilities)
	require.Equal(t, "go", req.Client.ClientTracer.Language)
	require.Equal(t, "test-svc", req.Client.ClientTracer.Service)
	require.Equal(t, "test-env", req.Client.ClientTracer.Env)
	require.Equal(t, "tracer-version", req.Client.ClientTracer.TracerVersion)
	require.Equal(t, "app-version", req.Client.ClientTracer.AppVersion)
	require.True(t, req.Client.IsTracer)
}

// TestAsync starts many goroutines that use the exported client API to make sure no deadlocks occur
func TestAsync(t *testing.T) {
	require.NoError(t, Start(DefaultClientConfig()))
	defer Stop()
	const iterations = 10000
	var wg sync.WaitGroup

	// Subscriptions
	for i := 0; i < iterations; i++ {
		product := fmt.Sprintf("%d", rand.Int()%10)
		capability := Capability(rand.Uint32() % 10)
		wg.Add(1)
		go func() {
			callback := func(update ProductUpdate) map[string]rc.ApplyStatus { return nil }
			Subscribe(product, callback, capability)
			wg.Done()
		}()
	}

	// Products
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RegisterProduct(fmt.Sprintf("%d", rand.Int()%10))
		}()
	}
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			UnregisterProduct(fmt.Sprintf("%d", rand.Int()%10))
		}()
	}

	// Capabilities
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RegisterCapability(Capability(rand.Uint32() % 10))
		}()
	}
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			UnregisterCapability(Capability(rand.Uint32() % 10))
		}()
	}

	// Callbacks
	callback := func(updates map[string]ProductUpdate) map[string]rc.ApplyStatus { return nil }
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RegisterCallback(callback)
		}()
	}
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			UnregisterCallback(callback)
		}()
	}
	wg.Wait()
}
