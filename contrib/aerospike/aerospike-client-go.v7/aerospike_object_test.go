// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike

import (
	"testing"

	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// testRecord is a struct used by object-API integration tests.
// Fields are mapped to Aerospike bins via `as:` tags.
type testRecord struct {
	Value int `as:"value"`
}

func TestPutObject(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	key := newKey(t, "put-object-test")
	err := client.PutObject(nil, key, &testRecord{Value: 42})
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "PutObject")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestGetObject(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	key := newKey(t, "get-object-test")
	_ = client.PutObject(nil, key, &testRecord{Value: 7})
	mt.Reset()

	var got testRecord
	err := client.GetObject(nil, key, &got)
	assert.NoError(t, err)
	assert.Equal(t, 7, got.Value)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "GetObject")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestBatchGetObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	keys := make([]*as.Key, 2)
	for i := range keys {
		keys[i] = newKey(t, "batch-get-obj-"+string(rune('a'+i)))
		_ = client.PutObject(nil, keys[i], &testRecord{Value: i})
	}
	mt.Reset()

	objects := []interface{}{new(testRecord), new(testRecord)}
	_, err := client.BatchGetObjects(nil, keys, objects)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "BatchGetObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestScanAllObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	objChan := make(chan *testRecord, 64)
	rs, err := client.ScanAllObjects(nil, objChan, testNamespace, testSet)
	if rs != nil {
		rs.Close()
	}
	for range objChan {
	}
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "ScanAllObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestScanPartitionObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	objChan := make(chan *testRecord, 64)
	rs, err := client.ScanPartitionObjects(nil, objChan, as.NewPartitionFilterAll(), testNamespace, testSet)
	if rs != nil {
		rs.Close()
	}
	for range objChan {
	}
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "ScanPartitionObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestQueryObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	stmt := as.NewStatement(testNamespace, testSet)
	objChan := make(chan *testRecord, 64)
	rs, err := client.QueryObjects(nil, stmt, objChan)
	if rs != nil {
		rs.Close()
	}
	for range objChan {
	}
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "QueryObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestQueryPartitionObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	stmt := as.NewStatement(testNamespace, testSet)
	objChan := make(chan *testRecord, 64)
	rs, err := client.QueryPartitionObjects(nil, stmt, objChan, as.NewPartitionFilterAll())
	if rs != nil {
		rs.Close()
	}
	for range objChan {
	}
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "QueryPartitionObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestScanNodeObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	nodes := client.GetNodes()
	require.NotEmpty(t, nodes, "cluster must have at least one node")

	objChan := make(chan *testRecord, 64)
	rs, err := client.ScanNodeObjects(nil, nodes[0], objChan, testNamespace, testSet)
	if rs != nil {
		rs.Close()
	}
	for range objChan {
	}
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "ScanNodeObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}

func TestQueryNodeObjects(t *testing.T) {
	requireIntegration(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := getClient(t, WithService("test-aerospike"))
	defer client.Close()

	nodes := client.GetNodes()
	require.NotEmpty(t, nodes, "cluster must have at least one node")

	stmt := as.NewStatement(testNamespace, testSet)
	objChan := make(chan *testRecord, 64)
	rs, err := client.QueryNodeObjects(nil, nodes[0], stmt, objChan)
	if rs != nil {
		rs.Close()
	}
	for range objChan {
	}
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	validateAerospikeSpan(t, spans[0], "QueryNodeObjects")
	assert.Equal(t, "test-aerospike", spans[0].Tag(ext.ServiceName))
}
