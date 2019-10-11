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

func TestClient(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	assert := assert.New(t)
	client, err := api.NewClient(&api.Config{HttpClient: NewHTTPClient()})
	assert.NoError(err)
	assert.NotNil(client)
	assert.Nil(err)
}

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
	ts, cleanupTs := setupServer(t)
	defer cleanupTs()

	client, err := setupClient(ts)
	if err != nil {
		t.Fatal(err)
	}
	testMountReadWrite(client, t)
}

func TestWrapHTTPClient(t *testing.T) {
	ts, cleanupTs := setupServer(t)
	defer cleanupTs()

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
	assert := assert.New(t)
	key := secretMountPath + "/test"
	fullPath := "/v1" + key
	data := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}

	t.Run("mount", func(t *testing.T) {
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
		assert.Zero(span.Tag(ext.Error))
		assert.Zero(span.Tag(ext.ErrorMsg))
		assert.Zero(span.Tag("vault.namespace"))
	})

	t.Run("write", func(t *testing.T) {
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
		assert.Zero(span.Tag(ext.Error))
		assert.Zero(span.Tag(ext.ErrorMsg))
		assert.Zero(span.Tag("vault.namespace"))
	})

	t.Run("read", func(t *testing.T) {
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
		assert.Zero(span.Tag(ext.Error))
		assert.Zero(span.Tag(ext.ErrorMsg))
		assert.Zero(span.Tag("vault.namespace"))
	})
}

func TestReadError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ts, cleanupTs := setupServer(t)
	defer cleanupTs()
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
	assert.NotZero(span.Tag(ext.ErrorMsg))
	assert.Zero(span.Tag("vault.namespace"))
}

func TestNamespace(t *testing.T) {
	assert := assert.New(t)

	ts, cleanupTs := setupServer(t)
	defer cleanupTs()
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
		assert.Zero(span.Tag(ext.Error))
		assert.Zero(span.Tag(ext.ErrorMsg))
		assert.Equal(namespace, span.Tag("vault.namespace"))
	})

	t.Run("read", func(t *testing.T) {
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
		assert.Zero(span.Tag(ext.Error))
		assert.Zero(span.Tag(ext.ErrorMsg))
		assert.Equal(namespace, span.Tag("vault.namespace"))
	})
}

func getWriteSpan(t *testing.T, conf *api.Config) mocktracer.Span {
	assert := assert.New(t)
	client, err := api.NewClient(conf)
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
	return spans[0]
}

func TestOptionServiceName(t *testing.T) {
	assert := assert.New(t)

	ts, cleanupTs := setupServer(t)
	defer cleanupTs()

	t.Run("WithServiceName", func(t *testing.T) {
		// Check default
		config := &api.Config{
			HttpClient: NewHTTPClient(),
			Address:    ts.URL,
		}
		span := getWriteSpan(t, config)
		assert.Equal(serviceName, span.Tag(ext.ServiceName))

		customServiceName := "someServiceName"
		config = &api.Config{
			HttpClient: NewHTTPClient(WithServiceName(customServiceName)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		assert.Equal(customServiceName, span.Tag(ext.ServiceName))
	})

	t.Run("WithAnalytics", func(t *testing.T) {
		// With no option, default should be no tag.
		config := &api.Config{
			HttpClient: NewHTTPClient(),
			Address:    ts.URL,
		}
		span := getWriteSpan(t, config)
		// Zero value for type (nil), not 0.0 float value
		assert.Zero(span.Tag(ext.EventSampleRate))

		// True should set the tag to 1.0
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalytics(true)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		assert.Equal(1.0, span.Tag(ext.EventSampleRate))

		// false should remove the tag.
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalytics(false)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		// Zero value for type (nil), not 0.0 float value
		assert.Zero(span.Tag(ext.EventSampleRate))

		//Last option should win
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalyticsRate(0.7), WithAnalytics(true)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		assert.Equal(1.0, span.Tag(ext.EventSampleRate))
	})

	t.Run("WithAnalyticsRate", func(t *testing.T) {
		//Negative Should be removed.
		config := &api.Config{
			HttpClient: NewHTTPClient(WithAnalyticsRate(-10.0)),
			Address:    ts.URL,
		}
		span := getWriteSpan(t, config)
		// Zero value for type (nil), not 0.0 float value
		assert.Zero(span.Tag(ext.EventSampleRate))

		// >1 Should be removed.
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalyticsRate(10.0)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		// Zero value for type (nil), not 0.0 float value
		assert.Zero(span.Tag(ext.EventSampleRate))

		// Highest rate
		rate := 1.0
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalyticsRate(rate)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		assert.Equal(rate, span.Tag(ext.EventSampleRate))

		// Lowest rate
		rate = 0.0
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalyticsRate(rate)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		assert.Equal(rate, span.Tag(ext.EventSampleRate))

		// Last option should win.
		rate = 0.5
		config = &api.Config{
			HttpClient: NewHTTPClient(WithAnalytics(true), WithAnalyticsRate(rate)),
			Address:    ts.URL,
		}
		span = getWriteSpan(t, config)
		assert.Equal(rate, span.Tag(ext.EventSampleRate))
	})

}
