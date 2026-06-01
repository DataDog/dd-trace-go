// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike

import (
	"context"
	"math"
	"os"
	"testing"

	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
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

// Unit tests that do not require a running Aerospike server.

func newTestClient(opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	return &Client{ifc: &mockAsClient{}, cfg: cfg, context: context.Background()}
}

func TestConfigDefaults(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)

	assert.Equal(t, "aerospike", cfg.serviceName)
	assert.Equal(t, string(instrumentation.PackageAerospikeClientGoV7), cfg.serviceSource)
	assert.Equal(t, "aerospike.command", cfg.operationName)
	assert.True(t, math.IsNaN(cfg.analyticsRate))
}

func TestWithServiceOption(t *testing.T) {
	cfg := new(clientConfig)
	defaults(cfg)
	WithService("custom").apply(cfg)

	assert.Equal(t, "custom", cfg.serviceName)
	assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, cfg.serviceSource)
}

func TestWithAnalyticsOption(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		cfg := new(clientConfig)
		defaults(cfg)
		WithAnalytics(true).apply(cfg)
		assert.Equal(t, 1.0, cfg.analyticsRate)
	})

	t.Run("disabled", func(t *testing.T) {
		cfg := new(clientConfig)
		defaults(cfg)
		WithAnalytics(false).apply(cfg)
		assert.True(t, math.IsNaN(cfg.analyticsRate))
	})
}

func TestWithAnalyticsRateOption(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg := new(clientConfig)
		defaults(cfg)
		WithAnalyticsRate(0.42).apply(cfg)
		assert.InDelta(t, 0.42, cfg.analyticsRate, 1e-9)
	})

	t.Run("out_of_range", func(t *testing.T) {
		cfg := new(clientConfig)
		defaults(cfg)
		WithAnalyticsRate(1.5).apply(cfg)
		assert.True(t, math.IsNaN(cfg.analyticsRate))
	})
}

func TestStartSpanTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	c := newTestClient(WithService("svc"))
	span := c.startSpan("Put")
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "Put")
	assert.Equal(t, "svc", spans[0].Tag(ext.ServiceName))
	assert.Nil(t, spans[0].Tag(ext.EventSampleRate))
}

func TestStartSpanAnalyticsRate(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	c := newTestClient(WithAnalyticsRate(0.5))
	span := c.startSpan("Get")
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.InDelta(t, 0.5, spans[0].Tag(ext.EventSampleRate), 1e-9)
}

func TestWithContextUnit(t *testing.T) {
	c := newTestClient()
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")

	c2 := c.WithContext(ctx)

	assert.Equal(t, ctx, c2.context)
	assert.Same(t, c.cfg, c2.cfg)
	assert.Equal(t, c.Client, c2.Client)
}

func TestWrappedMethods(t *testing.T) {
	key, err := as.NewKey("ns", "set", "pk")
	require.NoError(t, err)

	cases := []struct {
		name string
		fn   func(*Client)
	}{
		{"Put", func(c *Client) { c.Put(nil, key, nil) }},
		{"PutBins", func(c *Client) { c.PutBins(nil, key) }},
		{"Append", func(c *Client) { c.Append(nil, key, nil) }},
		{"AppendBins", func(c *Client) { c.AppendBins(nil, key) }},
		{"Prepend", func(c *Client) { c.Prepend(nil, key, nil) }},
		{"PrependBins", func(c *Client) { c.PrependBins(nil, key) }},
		{"Add", func(c *Client) { c.Add(nil, key, nil) }},
		{"AddBins", func(c *Client) { c.AddBins(nil, key) }},
		{"Delete", func(c *Client) { c.Delete(nil, key) }},
		{"Touch", func(c *Client) { c.Touch(nil, key) }},
		{"Exists", func(c *Client) { c.Exists(nil, key) }},
		{"BatchExists", func(c *Client) { c.BatchExists(nil, []*as.Key{key}) }},
		{"Get", func(c *Client) { c.Get(nil, key) }},
		{"GetHeader", func(c *Client) { c.GetHeader(nil, key) }},
		{"BatchGet", func(c *Client) { c.BatchGet(nil, []*as.Key{key}) }},
		{"BatchGetHeader", func(c *Client) { c.BatchGetHeader(nil, []*as.Key{key}) }},
		{"BatchGetOperate", func(c *Client) { c.BatchGetOperate(nil, []*as.Key{key}) }},
		{"Operate", func(c *Client) { c.Operate(nil, key) }},
		{"ScanAll", func(c *Client) { c.ScanAll(nil, "ns", "set") }},
		{"ScanPartitions", func(c *Client) { c.ScanPartitions(nil, nil, "ns", "set") }},
		{"BatchGetComplex", func(c *Client) { c.BatchGetComplex(nil, nil) }},
		{"BatchDelete", func(c *Client) { c.BatchDelete(nil, nil, []*as.Key{key}) }},
		{"BatchOperate", func(c *Client) { c.BatchOperate(nil, nil) }},
		{"BatchExecute", func(c *Client) { c.BatchExecute(nil, nil, []*as.Key{key}, "pkg", "fn") }},
		{"Execute", func(c *Client) { c.Execute(nil, key, "pkg", "fn") }},
		{"ExecuteUDF", func(c *Client) { c.ExecuteUDF(nil, nil, "pkg", "fn") }},
		{"ExecuteUDFNode", func(c *Client) { c.ExecuteUDFNode(nil, nil, nil, "pkg", "fn") }},
		{"QueryExecute", func(c *Client) { c.QueryExecute(nil, nil, nil) }},
		{"QueryPartitions", func(c *Client) { c.QueryPartitions(nil, nil, nil) }},
		{"Query", func(c *Client) { c.Query(nil, nil) }},
		{"QueryNode", func(c *Client) { c.QueryNode(nil, nil, nil) }},
		{"ScanNode", func(c *Client) { c.ScanNode(nil, nil, "ns", "set") }},
		{"Truncate", func(c *Client) { c.Truncate(nil, "ns", "set", nil) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			c := newTestClient()
			tc.fn(c)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			validateAerospikeSpan(t, spans[0], tc.name)
		})
	}
}
