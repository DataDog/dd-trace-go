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

	vault "github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
)

func TestClient(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client, err := vault.NewClient(&vault.Config{HttpClient: NewHttpClient()})
	assert.NoError(err)

	assert.NotNil(client)
	assert.Nil(err)
}

func TestClientError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var config = &vault.Config{
		HttpClient: NewHttpClient(),
		Address:    "http://bad host.com",
	}

	client, err := vault.NewClient(config)
	assert.Nil(client)
	assert.Error(err)
}

const (
	secretMountPath = "/ns1/ns2/secret"
)

func setupServer(t *testing.T) *httptest.Server {
	storage := make(map[string]string)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			fmt.Fprintln(w, "{}")
			defer r.Body.Close()
			bodyBytes, err := ioutil.ReadAll(r.Body)
			body := string(bodyBytes)
			if err != nil {
				t.Error(err)
				return
			}
			storage[r.URL.Path] = body
		} else if r.Method == "GET" {
			if value, ok := storage[r.URL.Path]; ok {
				secret := vault.Secret{}
				secret.Data = make(map[string]interface{})
				json.Unmarshal([]byte(value), &secret.Data)
				secretJson, err := json.Marshal(secret)
				if err != nil {
					t.Error(err)
					return
				}
				fmt.Fprintf(w, "%s\n", secretJson)
			} else {
				http.Error(w, "No data for key.", http.StatusNotFound)
			}
		}
	}))
	return ts
}

func cleanupServer(ts *httptest.Server) {
	ts.Close()
}

func setupVault(ts *httptest.Server) (*vault.Client, error) {
	var config = &vault.Config{
		HttpClient: NewHttpClient(),
		Address:    ts.URL,
	}

	client, err := vault.NewClient(config)
	client.SetToken("myroot")

	secretMount := vault.MountInput{
		Type:        "kv",
		Description: "Test KV Store",
		Local:       true,
	}
	err = client.Sys().Mount(secretMountPath, &secretMount)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func cleanupVault(client *vault.Client) {
	client.Sys().Unmount(secretMountPath)
}

func TestMountReadWrite(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ts := setupServer(t)
	defer cleanupServer(ts)
	client, err := setupVault(ts)
	if err != nil {
		t.Error(err)
		return
	}
	defer cleanupVault(client)

	key := secretMountPath + "/test"
	secretData := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
	secret, err := client.Logical().Write(key, secretData)
	if err != nil {
		t.Error(err)
		return
	}

	secret, err = client.Logical().Read(key)
	if err != nil {
		t.Error(err)
		return
	}

	assert.Equal(secret.Data["Key1"], secretData["Key1"])
	assert.Equal(secret.Data["Key2"], secretData["Key2"])

	spans := mt.FinishedSpans()
	assert.Len(spans, 3)
	mount := spans[0]
	write := spans[1]
	read := spans[2]

	fullPath := "/v1" + key

	// Mount operation
	assert.Equal("vault", mount.Tag(ext.ServiceName))
	assert.Equal("/v1/sys/mounts/ns1/ns2/secret", mount.Tag(ext.HTTPURL))
	assert.Equal(http.MethodPost, mount.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodPost+" /v1/sys/mounts/ns1/ns2/secret", mount.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeVault, mount.Tag(ext.SpanType))
	assert.Equal(200, mount.Tag(ext.HTTPCode))
	assert.Zero(mount.Tag(ext.Error))
	assert.Zero(mount.Tag(ext.ErrorDetails))
	assert.Zero(mount.Tag("vault.namespace"))

	// Write key
	assert.Equal("vault", write.Tag(ext.ServiceName))
	assert.Equal(fullPath, write.Tag(ext.HTTPURL))
	assert.Equal(http.MethodPut, write.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodPut+" "+fullPath, write.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeVault, write.Tag(ext.SpanType))
	assert.Equal(200, write.Tag(ext.HTTPCode))
	assert.Zero(write.Tag(ext.Error))
	assert.Zero(write.Tag(ext.ErrorDetails))
	assert.Zero(write.Tag("vault.namespace"))

	// Read key
	assert.Equal("vault", read.Tag(ext.ServiceName))
	assert.Equal(fullPath, read.Tag(ext.HTTPURL))
	assert.Equal(http.MethodGet, read.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodGet+" "+fullPath, read.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeVault, read.Tag(ext.SpanType))
	assert.Equal(200, read.Tag(ext.HTTPCode))
	assert.Zero(read.Tag(ext.Error))
	assert.Zero(read.Tag(ext.ErrorDetails))
	assert.Zero(read.Tag("vault.namespace"))
}

func TestReadError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ts := setupServer(t)
	defer cleanupServer(ts)
	client, err := setupVault(ts)
	if err != nil {
		t.Error(err)
		return
	}
	defer cleanupVault(client)

	key := "/some/bad/key"
	secret, err := client.Logical().Read(key)
	if err == nil {
		t.Errorf("Expected error when reading key from %s, but it returned: %#v", key, secret)
		return
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
	assert.Equal(ext.SpanTypeVault, readErr.Tag(ext.SpanType))
	assert.Equal(404, readErr.Tag(ext.HTTPCode))
	assert.NotZero(readErr.Tag(ext.Error))
	assert.NotZero(readErr.Tag(ext.ErrorDetails))
	assert.Zero(readErr.Tag("vault.namespace"))
}

func TestNamespace(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ts := setupServer(t)
	defer cleanupServer(ts)
	client, err := setupVault(ts)
	if err != nil {
		t.Error(err)
		return
	}
	defer cleanupVault(client)

	namespace := "/some/namespace"
	client.SetNamespace(namespace)

	key := secretMountPath + "/testNamespace"
	secretData := map[string]interface{}{"Key1": "Val1", "Key2": "Val2"}
	_, err = client.Logical().Write(key, secretData)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = client.Logical().Read(key)
	if err != nil {
		t.Error(err)
		return
	}

	spans := mt.FinishedSpans()
	assert.Len(spans, 3)
	writeWithNamespace := spans[1]
	readWithNamespace := spans[2]

	fullPath := "/v1" + key

	// Write key with namespace
	assert.Equal("vault", writeWithNamespace.Tag(ext.ServiceName))
	assert.Equal(fullPath, writeWithNamespace.Tag(ext.HTTPURL))
	assert.Equal(http.MethodPut, writeWithNamespace.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodPut+" "+fullPath, writeWithNamespace.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeVault, writeWithNamespace.Tag(ext.SpanType))
	assert.Equal(200, writeWithNamespace.Tag(ext.HTTPCode))
	assert.Zero(writeWithNamespace.Tag(ext.Error))
	assert.Zero(writeWithNamespace.Tag(ext.ErrorDetails))
	assert.Equal(namespace, writeWithNamespace.Tag("vault.namespace"))

	// Read key with namespace
	assert.Equal("vault", readWithNamespace.Tag(ext.ServiceName))
	assert.Equal(fullPath, readWithNamespace.Tag(ext.HTTPURL))
	assert.Equal(http.MethodGet, readWithNamespace.Tag(ext.HTTPMethod))
	assert.Equal(http.MethodGet+" "+fullPath, readWithNamespace.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeVault, readWithNamespace.Tag(ext.SpanType))
	assert.Equal(200, readWithNamespace.Tag(ext.HTTPCode))
	assert.Zero(readWithNamespace.Tag(ext.Error))
	assert.Zero(readWithNamespace.Tag(ext.ErrorDetails))
	assert.Equal(namespace, readWithNamespace.Tag("vault.namespace"))
}
