package consul

import (
	"fmt"
	"os"
	"testing"

	consul "github.com/hashicorp/consul/api"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

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

func kv() *KV {
	client, err := NewClient(consul.DefaultConfig())
	if err != nil {
		panic(err)
	}
	// Get a handle to the KV API
	kv := client.KV()
	return kv
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

func TestClient_error(t *testing.T) {
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

func TestClient_KV(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client, _ := NewClient(consul.DefaultConfig())
	clientCtx := client.ctx
	kv := client.KV()
	kvCtx := kv.ctx

	assert.Equal(clientCtx, kvCtx)
}

func TestKV_Put(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	p := &consul.KVPair{Key: "test_key", Value: []byte("test_value")}
	kv.Put(p, nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("PUT", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_Get(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	kv.Get("test", nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("GET", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_List(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	kv.List("test", nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("LIST", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_Keys(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	kv.Keys("test", "", nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("KEYS", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_CAS(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	p := &consul.KVPair{Key: "test_key", Value: []byte("test_value")}
	kv.CAS(p, nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("CAS", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_Acquire(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	p := &consul.KVPair{Key: "test_key", Value: []byte("test_value")}
	kv.Acquire(p, nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("ACQUIRE", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_Release(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	p := &consul.KVPair{Key: "test_key", Value: []byte("test_value")}
	kv.Release(p, nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("RELEASE", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_Delete(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	kv.Delete("test", nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("DELETE", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_DeleteCAS(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	p := &consul.KVPair{Key: "test_key", Value: []byte("test_value")}
	kv.DeleteCAS(p, nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("DELETECAS", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}

func TestKV_DeleteTree(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	kv := kv()

	kv.DeleteTree("test", nil)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal("consul.command", span.OperationName())
	assert.Equal("DELETETREE", span.Tag(ext.ResourceName))
	assert.Equal(ext.SpanTypeConsul, span.Tag(ext.SpanType))
	assert.Equal("consul", span.Tag(ext.ServiceName))
}
