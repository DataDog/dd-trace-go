package consul

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	consul "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestClient(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client, err := NewClient(consul.DefaultConfig())
	if err != nil {
		panic(err)
	}

	assert.NotNil(client)
	assert.Nil(err)
}

func TestClientError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var config = &consul.Config{
		Address: "badscheme://baduri",
	}

	client, err := NewClient(config)
	assert.Nil(client)
	assert.Error(err)

}

func TestClientKV(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client, _ := NewClient(consul.DefaultConfig())
	kv := client.KV()

	assert.Equal(client.ctx, kv.ctx)
}

func TestKV(t *testing.T) {
	key := "test.key"
	pair := &consul.KVPair{Key: key, Value: []byte("test_value")}
	for name, testFunc := range map[string]func(kv *KV){
		"Put":        func(kv *KV) { kv.Put(pair, nil) },
		"Get":        func(kv *KV) { kv.Get(key, nil) },
		"List":       func(kv *KV) { kv.List(key, nil) },
		"Keys":       func(kv *KV) { kv.Keys(key, "", nil) },
		"CAS":        func(kv *KV) { kv.CAS(pair, nil) },
		"Acquire":    func(kv *KV) { kv.Acquire(pair, nil) },
		"Release":    func(kv *KV) { kv.Release(pair, nil) },
		"Delete":     func(kv *KV) { kv.Delete(key, nil) },
		"DeleteCAS":  func(kv *KV) { kv.DeleteCAS(pair, nil) },
		"DeleteTree": func(kv *KV) { kv.DeleteTree(key, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()
			client, err := NewClient(consul.DefaultConfig())
			if err != nil {
				panic(err)
			}
			kv := client.KV()

			testFunc(kv)

			spans := mt.FinishedSpans()
			assert.Len(spans, 1)
			span := spans[0]
			assert.Equal("consul.command", span.OperationName())
			assert.Equal(strings.ToUpper(name), span.Tag(ext.ResourceName))
			assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
			assert.Equal("consul", span.Tag(ext.ServiceName))
			assert.Equal(key, span.Tag("consul.key"))
		})
	}
}
