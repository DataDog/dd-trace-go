// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
	client, err := NewClient(cfg)
	require.NoError(t, err)

	t.Run("registerCallback", func(t *testing.T) {
		client.callbacks = map[string][]Callback{}
		nilCallback := func(ProductUpdate) map[string]rc.ApplyStatus { return nil }
		defer func() { client.callbacks = map[string][]Callback{} }()
		require.Equal(t, 0, len(client.callbacks))
		client.RegisterCallback(nilCallback, rc.ProductASMFeatures)
		require.Equal(t, 1, len(client.callbacks[rc.ProductASMFeatures]))
		require.Equal(t, 1, len(client.callbacks))
		client.RegisterCallback(nilCallback, rc.ProductASMFeatures)
		require.Equal(t, 2, len(client.callbacks[rc.ProductASMFeatures]))
		require.Equal(t, 1, len(client.callbacks))
	})

	t.Run("apply-update", func(t *testing.T) {
		client.callbacks = map[string][]Callback{}
		cfgPath := "datadog/2/ASM_FEATURES/asm_features_activation/config"
		client.Products = append(client.Products, rc.ProductASMFeatures)

		client.RegisterCallback(func(u ProductUpdate) map[string]rc.ApplyStatus {
			require.NotNil(t, u)
			require.NotNil(t, u[cfgPath])
			require.Equal(t, string(u[cfgPath]), "test")
			return map[string]rc.ApplyStatus{cfgPath: {State: rc.ApplyStateAcknowledged}}
		}, rc.ProductASMFeatures)

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
