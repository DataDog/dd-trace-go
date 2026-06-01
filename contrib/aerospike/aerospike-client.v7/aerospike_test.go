// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike

import (
	"context"
	"os"
	"testing"

	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

const (
	testHost      = "127.0.0.1"
	testPort      = 3000
	testNamespace = "test"
	testSet       = "testset"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
}

func getClient(t *testing.T, opts ...ClientOption) *Client {
	t.Helper()
	raw, err := as.NewClient(testHost, testPort)
	require.NoError(t, err)
	return WrapClient(raw, opts...)
}

func newKey(t *testing.T, pk string) *as.Key {
	t.Helper()
	key, err := as.NewKey(testNamespace, testSet, pk)
	require.NoError(t, err)
	return key
}

func validateAerospikeSpan(t *testing.T, span *mocktracer.Span, resourceName string) {
	t.Helper()
	assert.Equal(t, "aerospike.command", span.OperationName(),
		"operation name should be aerospike.command")
	assert.Equal(t, resourceName, span.Tag(ext.ResourceName),
		"resource name should match the operation")
	assert.Equal(t, componentName, span.Tag(ext.Component),
		"component should be set to aerospike component name")
	assert.Equal(t, componentName, span.Integration(),
		"integration should be set to aerospike component name")
	assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind),
		"span.kind should be set to client")
	assert.Equal(t, "aerospike", span.Tag(ext.DBSystem),
		"db.system should be set to aerospike")
	assert.Equal(t, ext.SpanTypeAerospike, span.Tag(ext.SpanType),
		"span type should be aerospike")
}

func TestPut(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	key := newKey(t, "put-test")
	err := client.Put(nil, key, as.BinMap{"value": 1})
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "Put")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestGet(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	key := newKey(t, "get-test")
	_ = client.Put(nil, key, as.BinMap{"value": 42})
	mt.Reset()

	record, err := client.Get(nil, key)
	assert.NoError(t, err)
	assert.NotNil(t, record)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "Get")
}

func TestDelete(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	key := newKey(t, "delete-test")
	_ = client.Put(nil, key, as.BinMap{"value": 1})
	mt.Reset()

	_, err := client.Delete(nil, key)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "Delete")
}

func TestExists(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	key := newKey(t, "exists-test")
	_ = client.Put(nil, key, as.BinMap{"value": 1})
	mt.Reset()

	exists, err := client.Exists(nil, key)
	assert.NoError(t, err)
	assert.True(t, exists)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "Exists")
}

func TestWithContext(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	ctx := context.Background()
	span, ctx := tracer.StartSpanFromContext(ctx, "parent")

	key := newKey(t, "ctx-test")
	err := client.WithContext(ctx).Put(nil, key, as.BinMap{"value": 1})
	assert.NoError(t, err)

	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	validateAerospikeSpan(t, spans[0], "Put")
	assert.Equal(t, span, spans[1].Unwrap())
	assert.Equal(t, spans[1].TraceID(), spans[0].TraceID(),
		"aerospike span should be part of the parent trace")
}

func TestAnalyticsSettings(t *testing.T) {
	requireIntegration(t)

	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {
		t.Helper()
		client := getClient(t, opts...)
		defer client.Close()

		key := newKey(t, "analytics-test")
		err := client.Put(nil, key, as.BinMap{"value": 1})
		assert.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, rate, spans[0].Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)
		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		testutils.SetGlobalAnalyticsRate(t, 0.4)
		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

func TestWithService(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("my-aerospike"))
	defer client.Close()

	key := newKey(t, "service-test")
	err := client.Put(nil, key, as.BinMap{"value": 1})
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "my-aerospike", spans[0].Tag(ext.ServiceName))
}
