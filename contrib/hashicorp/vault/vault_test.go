// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package vault

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
)

const secretMountPath = "/ns1/ns2/secret"

func setupServer(t *testing.T) (*httptest.Server, func()) {
	storage := make(map[string]string)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			slurp, err := ioutil.ReadAll(r.Body)
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

func setupClient(ts *httptest.Server) (*api.Client, error) {
	config := &api.Config{
		HttpClient: NewHTTPClient(),
		Address:    ts.URL,
	}
	client, err := api.NewClient(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func TestNewHTTPClient(t *testing.T) {
	ts, cleanup := setupServer(t)
	defer cleanup()

	client, err := setupClient(ts)
	if err != nil {
		t.Fatal(err)
	}
	testMountReadWrite(client, t)
}

func TestWrapHTTPClient(t *testing.T) {
	ts, cleanup := setupServer(t)
	defer cleanup()

	httpClient := http.Client{}
	config := &api.Config{
		HttpClient: WrapHTTPClient(&httpClient),
		Address:    ts.URL,
	}
	client, err := api.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("myroot")

	testMountReadWrite(client, t)
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

func testMountReadWrite(c *api.Client, t *testing.T) {
	key := secretMountPath + "/test"
	fullPath := "/v1" + key
	data := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}

	t.Run("mount", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		defer mountKV(c, t)()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]

		// Mount operation
		assert.Equal("vault", span.Tag(ext.ServiceName))
		assert.Equal("/v1/sys/mounts/ns1/ns2/secret", span.Tag(ext.HTTPURL))
		assert.Equal(http.MethodPost, span.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodPost+" /v1/sys/mounts/ns1/ns2/secret", span.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal(200, span.Tag(ext.HTTPCode))
		assert.Nil(span.Tag(ext.Error))
		assert.Nil(span.Tag(ext.ErrorMsg))
		assert.Nil(span.Tag("vault.namespace"))
	})

	t.Run("write", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		defer mountKV(c, t)()

		// Write key
		_, err := c.Logical().Write(key, data)
		if err != nil {
			t.Fatal(err)
		}
		spans := mt.FinishedSpans()
		assert.Len(spans, 2)
		span := spans[1]

		assert.Equal("vault", span.Tag(ext.ServiceName))
		assert.Equal(fullPath, span.Tag(ext.HTTPURL))
		assert.Equal(http.MethodPut, span.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodPut+" "+fullPath, span.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal(200, span.Tag(ext.HTTPCode))
		assert.Nil(span.Tag(ext.Error))
		assert.Nil(span.Tag(ext.ErrorMsg))
		assert.Nil(span.Tag("vault.namespace"))
	})

	t.Run("read", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()
		defer mountKV(c, t)()

		// Write the key first
		_, err := c.Logical().Write(key, data)
		if err != nil {
			t.Fatal(err)
		}
		// Read key
		secret, err := c.Logical().Read(key)
		if err != nil {
			t.Fatal(err)
		}
		spans := mt.FinishedSpans()
		assert.Len(spans, 3)
		span := spans[2]

		assert.Equal(secret.Data["Key1"], data["Key1"])
		assert.Equal(secret.Data["Key2"], data["Key2"])
		assert.Equal("vault", span.Tag(ext.ServiceName))
		assert.Equal(fullPath, span.Tag(ext.HTTPURL))
		assert.Equal(http.MethodGet, span.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodGet+" "+fullPath, span.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal(200, span.Tag(ext.HTTPCode))
		assert.Nil(span.Tag(ext.Error))
		assert.Nil(span.Tag(ext.ErrorMsg))
		assert.Nil(span.Tag("vault.namespace"))
	})
}

func TestReadError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ts, cleanup := setupServer(t)
	defer cleanup()
	client, err := setupClient(ts)
	if err != nil {
		t.Fatal(err)
	}
	defer mountKV(client, t)()

	key := "/some/bad/key"
	fullPath := "/v1" + key
	secret, err := client.Logical().Read(key)
	if err == nil {
		t.Fatalf("Expected error when reading key from %s, but it returned: %#v", key, secret)
	}
	spans := mt.FinishedSpans()
	assert.Len(spans, 2)
	span := spans[1]

	// Read key error
	assert.Equal("vault", span.Tag(ext.ServiceName))
	assert.Equal(fullPath, span.Tag(ext.HTTPURL))
	assert.Equal(http.MethodGet, span.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodGet+" "+fullPath, span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
	assert.Equal(404, span.Tag(ext.HTTPCode))
	assert.Equal(true, span.Tag(ext.Error))
	assert.NotNil(span.Tag(ext.ErrorMsg))
	assert.Nil(span.Tag("vault.namespace"))
}

func TestNamespace(t *testing.T) {
	ts, cleanup := setupServer(t)
	defer cleanup()
	client, err := setupClient(ts)
	if err != nil {
		t.Fatal(err)
	}
	defer mountKV(client, t)()

	namespace := "/some/namespace"
	client.SetNamespace(namespace)
	key := secretMountPath + "/testNamespace"
	fullPath := "/v1" + key

	t.Run("write", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// Write key with namespace
		data := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
		_, err = client.Logical().Write(key, data)
		if err != nil {
			t.Fatal(err)
		}
		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]

		assert.Equal("vault", span.Tag(ext.ServiceName))
		assert.Equal(fullPath, span.Tag(ext.HTTPURL))
		assert.Equal(http.MethodPut, span.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodPut+" "+fullPath, span.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal(200, span.Tag(ext.HTTPCode))
		assert.Nil(span.Tag(ext.Error))
		assert.Nil(span.Tag(ext.ErrorMsg))
		assert.Equal(namespace, span.Tag("vault.namespace"))
	})

	t.Run("read", func(t *testing.T) {
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		// Write key with namespace first
		data := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
		_, err = client.Logical().Write(key, data)
		if err != nil {
			t.Fatal(err)
		}
		// Read key with namespace
		_, err = client.Logical().Read(key)
		if err != nil {
			t.Fatal(err)
		}
		spans := mt.FinishedSpans()
		assert.Len(spans, 2)
		span := spans[1]

		assert.Equal("vault", span.Tag(ext.ServiceName))
		assert.Equal(fullPath, span.Tag(ext.HTTPURL))
		assert.Equal(http.MethodGet, span.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodGet+" "+fullPath, span.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, span.Tag(ext.SpanType))
		assert.Equal(200, span.Tag(ext.HTTPCode))
		assert.Nil(span.Tag(ext.Error))
		assert.Nil(span.Tag(ext.ErrorMsg))
		assert.Equal(namespace, span.Tag("vault.namespace"))
	})
}

func TestOption(t *testing.T) {
	ts, cleanup := setupServer(t)
	defer cleanup()

	for _, tt := range []struct {
		name string
		opts []Option
		test func(assert *assert.Assertions, span mocktracer.Span)
	}{
		{
			name: "Default options",
			opts: []Option{},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal(defaultServiceName, span.Tag(ext.ServiceName))
				assert.Nil(span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "Custom service name",
			opts: []Option{WithServiceName("someServiceName")},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal("someServiceName", span.Tag(ext.ServiceName))
			},
		},
		{
			name: "WithAnalytics(true)",
			opts: []Option{WithAnalytics(true)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal(1.0, span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalytics(false)",
			opts: []Option{WithAnalytics(false)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Nil(span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalytics Last option wins",
			opts: []Option{WithAnalyticsRate(0.7), WithAnalytics(true)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal(1.0, span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalyticsRate Negative rate",
			opts: []Option{WithAnalyticsRate(-10.0)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Nil(span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalyticsRate Greater than 1 rate",
			opts: []Option{WithAnalyticsRate(10.0)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Nil(span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalyticsRate(1.0)",
			opts: []Option{WithAnalyticsRate(1.0)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal(1.0, span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalyticsRate(0.0)",
			opts: []Option{WithAnalyticsRate(0.0)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal(0.0, span.Tag(ext.EventSampleRate))
			},
		},
		{
			name: "WithAnalyticsRate Last option wins",
			opts: []Option{WithAnalytics(true), WithAnalyticsRate(0.7)},
			test: func(assert *assert.Assertions, span mocktracer.Span) {
				assert.Equal(0.7, span.Tag(ext.EventSampleRate))
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)
			config := &api.Config{
				HttpClient: NewHTTPClient(tt.opts...),
				Address:    ts.URL,
			}
			client, err := api.NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			defer mountKV(client, t)()

			mt := mocktracer.Start()
			defer mt.Stop()

			_, err = client.Logical().Write(
				secretMountPath+"/key",
				map[string]interface{}{"Key1": "Val1", "Key2": "Val2"},
			)
			if err != nil {
				t.Fatal(err)
			}
			spans := mt.FinishedSpans()
			assert.Len(spans, 1)
			span := spans[0]
			tt.test(assert, span)
		})
	}
}
