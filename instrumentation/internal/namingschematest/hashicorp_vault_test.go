// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vaulttrace "github.com/DataDog/dd-trace-go/contrib/hashicorp/vault/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const secretMountPath = "/ns1/ns2/secret"

func hashicorpVaultGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []vaulttrace.Option
		if serviceOverride != "" {
			opts = append(opts, vaulttrace.WithService(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()

		ts, cleanup := setupServer(t)
		defer cleanup()

		client, err := api.NewClient(&api.Config{
			HttpClient: vaulttrace.NewHTTPClient(opts...),
			Address:    ts.URL,
		})
		require.NoError(t, err)
		if err != nil {
			t.Fatal(err)
		}
		defer mountKV(client, t)()

		// Write key with namespace first
		data := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
		_, err = client.Logical().Write("/some/path", data)
		require.NoError(t, err)

		return mt.FinishedSpans()
	}
}

func setupServer(t *testing.T) (*httptest.Server, func()) {
	storage := make(map[string]string)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			slurp, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			storage[r.URL.Path] = string(slurp)
			fmt.Fprintln(w, "{}")
		case http.MethodGet:
			val, ok := storage[r.URL.Path]
			if !ok {
				http.Error(w, "No data for key.", http.StatusNotFound)
				return
			}
			secret := api.Secret{Data: make(map[string]interface{})}
			json.Unmarshal([]byte(val), &secret.Data)
			if err := json.NewEncoder(w).Encode(secret); err != nil {
				t.Fatal(err)
			}
		}
	}))
	return ts, func() {
		ts.Close()
	}
}

// mountKV mounts the K/V engine on secretMountPath and returns a function to unmount it.
// See: https://www.vaultproject.io/docs/secrets/
func mountKV(c *api.Client, t *testing.T) func() {
	secretMount := api.MountInput{
		Type:        "kv",
		Description: "Test KV Store",
		Local:       true,
	}
	if err := c.Sys().Mount(secretMountPath, &secretMount); err != nil {
		t.Fatal(err)
	}
	return func() {
		c.Sys().Unmount(secretMountPath)
	}
}

var hashicorpVault = harness.TestCase{
	Name:     instrumentation.PackageHashicorpVaultAPI,
	GenSpans: hashicorpVaultGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("vault", 2),
		DDService:       harness.RepeatString("vault", 2),
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 2),
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "http.request", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "vault.query", spans[0].OperationName())
	},
}
