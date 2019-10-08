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

const (
	secretMountPath = "/ns1/ns2/secret"
)

func TestClient(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	t.Run("ok", func(t *testing.T) {
		assert := assert.New(t)
		client, err := api.NewClient(&api.Config{HttpClient: NewHTTPClient()})
		assert.NoError(err)
		assert.NotNil(client)
		assert.Nil(err)
	})

	t.Run("error", func(t *testing.T) {
		assert := assert.New(t)
		var config = &api.Config{
			HttpClient: NewHTTPClient(),
			Address:    "http://bad host.com",
		}
		client, err := api.NewClient(config)
		assert.Nil(client)
		assert.Error(err)
	})
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
			value, ok := storage[r.URL.Path]
			if !ok {
				http.Error(w, "No data for key.", http.StatusNotFound)
				return
			}
			secret := api.Secret{}
			secret.Data = make(map[string]interface{})
			json.Unmarshal([]byte(value), &secret.Data)
			secretJson, err := json.Marshal(secret)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(w, "%s\n", secretJson)
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

func TestWrapHTTPTransport(t *testing.T) {
	ts, cleanupTs := setupServer(t)
	defer cleanupTs()

	httpClient := http.Client{}
	config := &api.Config{
		HttpClient: WrapHTTPTransport(&httpClient),
		Address:    ts.URL,
	}

	client, err := api.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("myroot")

	testMountReadWrite(client, t)
}

func mountKV(client *api.Client, t *testing.T) func() {
	secretMount := api.MountInput{
		Type:        "kv",
		Description: "Test KV Store",
		Local:       true,
	}

	if err := client.Sys().Mount(secretMountPath, &secretMount); err != nil {
		t.Fatal(err)
	}

	return func() {
		client.Sys().Unmount(secretMountPath)
	}
}

func testMountReadWrite(client *api.Client, t *testing.T) {
	assert := assert.New(t)
	key := secretMountPath + "/test"
	fullPath := "/v1" + key
	secretData := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}

	t.Run("mount", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		defer mountKV(client, t)()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		mount := spans[0]

		// Mount operation
		assert.Equal("vault", mount.Tag(ext.ServiceName))
		assert.Equal("/v1/sys/mounts/ns1/ns2/secret", mount.Tag(ext.HTTPURL))
		assert.Equal(http.MethodPost, mount.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodPost+" /v1/sys/mounts/ns1/ns2/secret", mount.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, mount.Tag(ext.SpanType))
		assert.Equal(200, mount.Tag(ext.HTTPCode))
		assert.Zero(mount.Tag(ext.Error))
		assert.Zero(mount.Tag(ext.ErrorDetails))
		assert.Zero(mount.Tag("vault.namespace"))
	})

	t.Run("write", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		defer mountKV(client, t)()

		// Write key
		_, err := client.Logical().Write(key, secretData)
		if err != nil {
			t.Fatal(err)
		}

		spans := mt.FinishedSpans()
		assert.Len(spans, 2)
		write := spans[1]

		assert.Equal("vault", write.Tag(ext.ServiceName))
		assert.Equal(fullPath, write.Tag(ext.HTTPURL))
		assert.Equal(http.MethodPut, write.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodPut+" "+fullPath, write.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, write.Tag(ext.SpanType))
		assert.Equal(200, write.Tag(ext.HTTPCode))
		assert.Zero(write.Tag(ext.Error))
		assert.Zero(write.Tag(ext.ErrorDetails))
		assert.Zero(write.Tag("vault.namespace"))
	})

	t.Run("read", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		defer mountKV(client, t)()

		// Write the key first
		_, err := client.Logical().Write(key, secretData)
		if err != nil {
			t.Fatal(err)
		}

		// Read key
		secret, err := client.Logical().Read(key)
		if err != nil {
			t.Fatal(err)
		}

		assert.Equal(secret.Data["Key1"], secretData["Key1"])
		assert.Equal(secret.Data["Key2"], secretData["Key2"])

		spans := mt.FinishedSpans()
		assert.Len(spans, 3)
		read := spans[2]

		assert.Equal("vault", read.Tag(ext.ServiceName))
		assert.Equal(fullPath, read.Tag(ext.HTTPURL))
		assert.Equal(http.MethodGet, read.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodGet+" "+fullPath, read.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, read.Tag(ext.SpanType))
		assert.Equal(200, read.Tag(ext.HTTPCode))
		assert.Zero(read.Tag(ext.Error))
		assert.Zero(read.Tag(ext.ErrorDetails))
		assert.Zero(read.Tag("vault.namespace"))
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
	secret, err := client.Logical().Read(key)
	if err == nil {
		t.Fatalf("Expected error when reading key from %s, but it returned: %#v", key, secret)
	}

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)
	readErr := spans[1]

	fullPath := "/v1" + key

	// Read key error
	assert.Equal("vault", readErr.Tag(ext.ServiceName))
	assert.Equal(fullPath, readErr.Tag(ext.HTTPURL))
	assert.Equal(http.MethodGet, readErr.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodGet+" "+fullPath, readErr.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeHTTP, readErr.Tag(ext.SpanType))
	assert.Equal(404, readErr.Tag(ext.HTTPCode))
	assert.NotZero(readErr.Tag(ext.Error))
	assert.NotZero(readErr.Tag(ext.ErrorDetails))
	assert.Zero(readErr.Tag("vault.namespace"))
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
		secretData := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
		_, err = client.Logical().Write(key, secretData)
		if err != nil {
			t.Fatal(err)
		}

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		writeWithNamespace := spans[0]

		assert.Equal("vault", writeWithNamespace.Tag(ext.ServiceName))
		assert.Equal(fullPath, writeWithNamespace.Tag(ext.HTTPURL))
		assert.Equal(http.MethodPut, writeWithNamespace.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodPut+" "+fullPath, writeWithNamespace.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, writeWithNamespace.Tag(ext.SpanType))
		assert.Equal(200, writeWithNamespace.Tag(ext.HTTPCode))
		assert.Zero(writeWithNamespace.Tag(ext.Error))
		assert.Zero(writeWithNamespace.Tag(ext.ErrorDetails))
		assert.Equal(namespace, writeWithNamespace.Tag("vault.namespace"))
	})

	t.Run("read", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Write key with namespace first
		secretData := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
		_, err = client.Logical().Write(key, secretData)
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
		readWithNamespace := spans[1]

		assert.Equal("vault", readWithNamespace.Tag(ext.ServiceName))
		assert.Equal(fullPath, readWithNamespace.Tag(ext.HTTPURL))
		assert.Equal(http.MethodGet, readWithNamespace.Tag(ext.HTTPMethod))
		assert.Equal(http.MethodGet+" "+fullPath, readWithNamespace.Tag(ext.ResourceName))
		assert.Equal(ext.SpanTypeHTTP, readWithNamespace.Tag(ext.SpanType))
		assert.Equal(200, readWithNamespace.Tag(ext.HTTPCode))
		assert.Zero(readWithNamespace.Tag(ext.Error))
		assert.Zero(readWithNamespace.Tag(ext.ErrorDetails))
		assert.Equal(namespace, readWithNamespace.Tag("vault.namespace"))
	})
}
