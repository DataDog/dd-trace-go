// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package remoteconfig

import (
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
	client := &Client{
		ClientConfig: cfg,
		clientID:     generateID(),
		endpoint:     fmt.Sprintf("http://%s/v0.7/config", cfg.AgentAddr),
		stop:         make(chan struct{}),
		callbacks:    map[string][]Callback{},
	}

	t.Run("registerCallback", func(t *testing.T) {
		defer func() { client.callbacks = map[string][]Callback{} }()
		require.Equal(t, 0, len(client.callbacks))
		client.RegisterCallback(func(ProductUpdate) {}, rc.ProductASMFeatures)
		require.Equal(t, 1, len(client.callbacks[rc.ProductASMFeatures]))
		require.Equal(t, 1, len(client.callbacks))
		client.RegisterCallback(func(ProductUpdate) {}, rc.ProductASMFeatures)
		require.Equal(t, 2, len(client.callbacks[rc.ProductASMFeatures]))
		require.Equal(t, 1, len(client.callbacks))
	})

	/*
		t.Run("apply-update", func(t *testing.T) {
			client.Products = append(client.Products, rc.ProductASMFeatures)
			client.RegisterCallback(func(u ProductUpdate) {
				require.NotNil(t, u)
				require.NotNil(t, u[rc.ProductASMFeatures])
				require.Equal(t, string(u[rc.ProductASMFeatures]), "test")
			}, rc.ProductASMFeatures)

			client.applyUpdate(&clientGetConfigsResponse{
				TargetFiles:   []*file{{Path: "path/to/ASM_FEATURES/config", Raw: []byte("test")}},
				ClientConfigs: []string{rc.ProductASMFeatures},
			})
		})
	*/
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
