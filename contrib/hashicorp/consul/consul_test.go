// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package consul

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	consul "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.NoError(err)

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
			assert.NoError(err)
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
			assert.Equal("hashicorp/consul", span.Tag(ext.Component))
			assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
			assert.Equal("127.0.0.1", span.Tag(ext.NetworkDestinationName))
			assert.Equal(ext.DBSystemConsulKV, span.Tag(ext.DBSystem))
		})
	}
}
func TestNamingSchema(t *testing.T) {
	genSpans := func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []ClientOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()
		client, err := NewClient(consul.DefaultConfig(), opts...)
		require.NoError(t, err)
		kv := client.KV()

		pair := &consul.KVPair{Key: "test.key", Value: []byte("test_value")}
		_, err = kv.Put(pair, nil)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	}
	assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "consul.command", spans[0].OperationName())
	}
	assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "consul.query", spans[0].OperationName())
	}
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"consul"},
		WithDDService:            []string{"consul"},
		WithDDServiceAndOverride: []string{namingschematest.TestServiceOverride},
	}
	t.Run("service name", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("operation name", namingschematest.NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
}
