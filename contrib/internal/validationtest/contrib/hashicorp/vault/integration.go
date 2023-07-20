// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package vault

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hashicorp/vault/api"
	vault "github.com/hashicorp/vault/api"
	vaulttrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/hashicorp/vault"
)

type Integration struct {
	client   *vault.Client
	server   *httptest.Server
	numSpans int
	opts     []vaulttrace.Option
}

func New() *Integration {
	return &Integration{
		opts: make([]vaulttrace.Option, 0),
	}
}

func (i *Integration) Name() string {
	return "hashicorp/vault"
}

func (i *Integration) Init(t *testing.T) {
	t.Helper()

	i.server = setupServer(t)

	var err error
	i.client, err = setupClient(i, i.server)
	require.NoError(t, err)

	t.Cleanup(func() {
		i.numSpans = 0
		i.server.Close()
	})
}

func (i *Integration) GenSpans(t *testing.T) {
	t.Helper()

	key := secretMountPath + "/test"
	data := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}

	// Write the key first
	_, err := i.client.Logical().Write(key, data)
	require.NoError(t, err)
	i.numSpans++

	_, err = i.client.Logical().Read(key)
	require.NoError(t, err)
	i.numSpans++
}

func (i *Integration) NumSpans() int {
	return i.numSpans
}

func (i *Integration) WithServiceName(name string) {
	i.opts = append(i.opts, vaulttrace.WithServiceName(name))
}

const secretMountPath = "/ns1/ns2/secret"

func setupServer(t *testing.T) *httptest.Server {
	storage := make(map[string]string)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			slurp, err := io.ReadAll(r.Body)
			require.NoError(t, err)
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
			err := json.NewEncoder(w).Encode(secret)
			require.NoError(t, err)
		}
	}))
	return ts
}

func setupClient(i *Integration, ts *httptest.Server) (*api.Client, error) {
	config := &api.Config{
		HttpClient: vaulttrace.NewHTTPClient(i.opts...),
		Address:    ts.URL,
	}
	client, err := api.NewClient(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}
