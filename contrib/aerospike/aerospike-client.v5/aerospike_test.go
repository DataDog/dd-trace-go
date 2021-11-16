// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

package aerospike

import (
	"fmt"
	"os"
	"testing"

	as "github.com/aerospike/aerospike-client-go/v5"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
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

	underlyingClient, err := as.NewClient("127.0.0.1", 3000)
	assert.Nil(err)
	client := WrapClient(underlyingClient, WithServiceName("my-service"))

	key1, err := as.NewKey("test", "", "key1")
	assert.Nil(err)
	err = client.Put(nil, key1, map[string]interface{}{
		"key": key1.Value(),
		"foo": "bar",
	})
	assert.Nil(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("aerospike.command", span.OperationName())
	assert.Equal(ext.SpanTypeAerospike, span.Tag(ext.SpanType))
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("Put", span.Tag(ext.ResourceName))
}

func TestCommandError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	underlyingClient, err := as.NewClient("127.0.0.1", 3000)
	assert.Nil(err)
	client := WrapClient(underlyingClient, WithServiceName("my-service"))

	key1, err := as.NewKey("non-existing-namespace", "", "key1")
	_, err = client.Get(nil, key1)
	assert.NotNil(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(err, span.Tag(ext.Error).(error))
	assert.Equal("aerospike.command", span.OperationName())
	assert.Equal(ext.SpanTypeAerospike, span.Tag(ext.SpanType))
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("Get", span.Tag(ext.ResourceName))
}
