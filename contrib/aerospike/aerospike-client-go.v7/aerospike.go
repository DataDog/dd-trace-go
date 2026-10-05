// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aerospike provides functions to trace the aerospike/aerospike-client-go package (https://github.com/aerospike/aerospike-client-go).
//
// WrapClient wraps an aerospike Client with one that traces every request. Use
// WithContext to link the spans to an existing trace:
//
//	ac := aerospike.WrapClient(client)
//	ac.WithContext(ctx).Put(nil, key, bins)
//
// Under Orchestrion, calls to the aerospike Client are rewritten to go through
// this wrapper automatically; see orchestrion.yml.
package aerospike // import "github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2"

import (
	"context"
	"reflect"
	"time"

	as "github.com/aerospike/aerospike-client-go/v7"

	"github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2/internal/tracing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// WrapClient wraps an aerospike.Client so that all requests are traced using
// the default tracer with the service name "aerospike".
func WrapClient(client *as.Client, opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	tracing.Instr.Logger().Debug("contrib/aerospike/aerospike-client-go.v7: Wrapping Client: %#v", cfg)
	return &Client{
		Client:  client,
		cfg:     cfg,
		context: context.Background(),
	}
}

// WrapClientContext returns a traced Client for client, bound to ctx and using
// the default configuration. A nil ctx is treated as context.Background().
//
// client may be an *as.Client or an as.Client value, so that one Orchestrion
// aspect can rewrite both receiver forms. A value receiver only reaches here when
// it is addressable, since that is all Go permits for a pointer-receiver method.
//
// It behaves like WrapClient(client).WithContext(ctx), but leaves the default
// configuration to be resolved when each span starts rather than here.
// Orchestrion builds a wrapper on every instrumented call, so this keeps that
// path to a single allocation.
func WrapClientContext[T as.Client | *as.Client](client T, ctx context.Context) *Client {
	if isNilContext(ctx) {
		ctx = context.Background()
	}
	traced := &Client{context: ctx}
	switch v := any(client).(type) {
	case *as.Client:
		traced.Client = v
	case as.Client:
		traced.Client = &v
	}
	return traced
}

// isNilContext reports whether ctx cannot be used. Besides a nil interface, the
// aspect may thread a parameter whose concrete type merely implements
// context.Context; a nil one of those is held in a non-nil interface and would
// panic on the first method call.
func isNilContext(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	v := reflect.ValueOf(ctx)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

// A Client is used to trace requests to the Aerospike server.
type Client struct {
	*as.Client
	cfg     *clientConfig
	context context.Context
}

// WithContext creates a copy of the Client with the given context.
func (c *Client) WithContext(ctx context.Context) *Client {
	return &Client{
		Client:  c.Client,
		cfg:     c.cfg,
		context: ctx,
	}
}

// startSpan starts a span from the context set with WithContext. A nil cfg means
// the client was built by WrapClientContext, which leaves the defaults to be
// resolved here so that later tracer configuration is picked up.
func (c *Client) startSpan(resourceName string) *tracer.Span {
	if c.cfg == nil {
		return tracing.StartDefaultSpan(c.context, resourceName)
	}
	return tracing.StartSpan(c.context, c.cfg.serviceName, c.cfg.serviceSource, c.cfg.operationName, resourceName)
}

// wrapped methods:

// Put invokes and traces Client.Put.
func (c *Client) Put(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Put")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Put(policy, key, binMap)
}

// PutBins invokes and traces Client.PutBins.
func (c *Client) PutBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("PutBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.PutBins(policy, key, bins...)
}

// Append invokes and traces Client.Append.
func (c *Client) Append(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Append")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Append(policy, key, binMap)
}

// AppendBins invokes and traces Client.AppendBins.
func (c *Client) AppendBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("AppendBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.AppendBins(policy, key, bins...)
}

// Prepend invokes and traces Client.Prepend.
func (c *Client) Prepend(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Prepend")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Prepend(policy, key, binMap)
}

// PrependBins invokes and traces Client.PrependBins.
func (c *Client) PrependBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("PrependBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.PrependBins(policy, key, bins...)
}

// Add invokes and traces Client.Add.
func (c *Client) Add(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Add")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Add(policy, key, binMap)
}

// AddBins invokes and traces Client.AddBins.
func (c *Client) AddBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("AddBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.AddBins(policy, key, bins...)
}

// Delete invokes and traces Client.Delete.
func (c *Client) Delete(policy *as.WritePolicy, key *as.Key) (existed bool, err as.Error) {
	span := c.startSpan("Delete")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Delete(policy, key)
}

// Touch invokes and traces Client.Touch.
func (c *Client) Touch(policy *as.WritePolicy, key *as.Key) (err as.Error) {
	span := c.startSpan("Touch")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Touch(policy, key)
}

// Exists invokes and traces Client.Exists.
func (c *Client) Exists(policy *as.BasePolicy, key *as.Key) (exists bool, err as.Error) {
	span := c.startSpan("Exists")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Exists(policy, key)
}

// BatchExists invokes and traces Client.BatchExists.
func (c *Client) BatchExists(policy *as.BatchPolicy, keys []*as.Key) (results []bool, err as.Error) {
	span := c.startSpan("BatchExists")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchExists(policy, keys)
}

// Get invokes and traces Client.Get.
func (c *Client) Get(policy *as.BasePolicy, key *as.Key, binNames ...string) (record *as.Record, err as.Error) {
	span := c.startSpan("Get")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Get(policy, key, binNames...)
}

// GetHeader invokes and traces Client.GetHeader.
func (c *Client) GetHeader(policy *as.BasePolicy, key *as.Key) (record *as.Record, err as.Error) {
	span := c.startSpan("GetHeader")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.GetHeader(policy, key)
}

// BatchGet invokes and traces Client.BatchGet.
func (c *Client) BatchGet(policy *as.BatchPolicy, keys []*as.Key, binNames ...string) (records []*as.Record, err as.Error) {
	span := c.startSpan("BatchGet")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGet(policy, keys, binNames...)
}

// BatchGetHeader invokes and traces Client.BatchGetHeader.
func (c *Client) BatchGetHeader(policy *as.BatchPolicy, keys []*as.Key) (records []*as.Record, err as.Error) {
	span := c.startSpan("BatchGetHeader")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetHeader(policy, keys)
}

// BatchGetOperate invokes and traces Client.BatchGetOperate.
func (c *Client) BatchGetOperate(policy *as.BatchPolicy, keys []*as.Key, ops ...*as.Operation) (records []*as.Record, err as.Error) {
	span := c.startSpan("BatchGetOperate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetOperate(policy, keys, ops...)
}

// Operate invokes and traces Client.Operate.
func (c *Client) Operate(policy *as.WritePolicy, key *as.Key, operations ...*as.Operation) (record *as.Record, err as.Error) {
	span := c.startSpan("Operate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Operate(policy, key, operations...)
}

// ScanAll invokes and traces Client.ScanAll.
func (c *Client) ScanAll(apolicy *as.ScanPolicy, namespace string, setName string, binNames ...string) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("ScanAll")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanAll(apolicy, namespace, setName, binNames...)
}

// ScanPartitions invokes and traces Client.ScanPartitions.
func (c *Client) ScanPartitions(apolicy *as.ScanPolicy, partitionFilter *as.PartitionFilter, namespace string, setName string, binNames ...string) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("ScanPartitions")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanPartitions(apolicy, partitionFilter, namespace, setName, binNames...)
}

// BatchGetComplex invokes and traces Client.BatchGetComplex.
func (c *Client) BatchGetComplex(policy *as.BatchPolicy, records []*as.BatchRead) (err as.Error) {
	span := c.startSpan("BatchGetComplex")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetComplex(policy, records)
}

// BatchDelete invokes and traces Client.BatchDelete.
func (c *Client) BatchDelete(policy *as.BatchPolicy, deletePolicy *as.BatchDeletePolicy, keys []*as.Key) (results []*as.BatchRecord, err as.Error) {
	span := c.startSpan("BatchDelete")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchDelete(policy, deletePolicy, keys)
}

// BatchOperate invokes and traces Client.BatchOperate.
func (c *Client) BatchOperate(policy *as.BatchPolicy, records []as.BatchRecordIfc) (err as.Error) {
	span := c.startSpan("BatchOperate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchOperate(policy, records)
}

// BatchExecute invokes and traces Client.BatchExecute.
func (c *Client) BatchExecute(policy *as.BatchPolicy, udfPolicy *as.BatchUDFPolicy, keys []*as.Key, packageName string, functionName string, args ...as.Value) (results []*as.BatchRecord, err as.Error) {
	span := c.startSpan("BatchExecute")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchExecute(policy, udfPolicy, keys, packageName, functionName, args...)
}

// Execute invokes and traces Client.Execute.
func (c *Client) Execute(policy *as.WritePolicy, key *as.Key, packageName string, functionName string, args ...as.Value) (result any, err as.Error) {
	span := c.startSpan("Execute")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Execute(policy, key, packageName, functionName, args...)
}

// ExecuteUDF invokes and traces Client.ExecuteUDF.
func (c *Client) ExecuteUDF(policy *as.QueryPolicy, statement *as.Statement, packageName string, functionName string, functionArgs ...as.Value) (task *as.ExecuteTask, err as.Error) {
	span := c.startSpan("ExecuteUDF")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ExecuteUDF(policy, statement, packageName, functionName, functionArgs...)
}

// ExecuteUDFNode invokes and traces Client.ExecuteUDFNode.
func (c *Client) ExecuteUDFNode(policy *as.QueryPolicy, node *as.Node, statement *as.Statement, packageName string, functionName string, functionArgs ...as.Value) (task *as.ExecuteTask, err as.Error) {
	span := c.startSpan("ExecuteUDFNode")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ExecuteUDFNode(policy, node, statement, packageName, functionName, functionArgs...)
}

// QueryExecute invokes and traces Client.QueryExecute.
func (c *Client) QueryExecute(policy *as.QueryPolicy, writePolicy *as.WritePolicy, statement *as.Statement, ops ...*as.Operation) (task *as.ExecuteTask, err as.Error) {
	span := c.startSpan("QueryExecute")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryExecute(policy, writePolicy, statement, ops...)
}

// QueryPartitions invokes and traces Client.QueryPartitions.
func (c *Client) QueryPartitions(policy *as.QueryPolicy, statement *as.Statement, partitionFilter *as.PartitionFilter) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("QueryPartitions")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryPartitions(policy, statement, partitionFilter)
}

// Query invokes and traces Client.Query.
func (c *Client) Query(policy *as.QueryPolicy, statement *as.Statement) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("Query")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Query(policy, statement)
}

// QueryNode invokes and traces Client.QueryNode.
func (c *Client) QueryNode(policy *as.QueryPolicy, node *as.Node, statement *as.Statement) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("QueryNode")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryNode(policy, node, statement)
}

// ScanNode invokes and traces Client.ScanNode.
func (c *Client) ScanNode(apolicy *as.ScanPolicy, node *as.Node, namespace string, setName string, binNames ...string) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("ScanNode")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanNode(apolicy, node, namespace, setName, binNames...)
}

// Truncate invokes and traces Client.Truncate.
func (c *Client) Truncate(policy *as.InfoPolicy, namespace, set string, beforeLastUpdate *time.Time) (err as.Error) {
	span := c.startSpan("Truncate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Truncate(policy, namespace, set, beforeLastUpdate)
}

// QueryAggregate invokes and traces Client.QueryAggregate.
func (c *Client) QueryAggregate(policy *as.QueryPolicy, statement *as.Statement, packageName, functionName string, functionArgs ...as.Value) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("QueryAggregate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryAggregate(policy, statement, packageName, functionName, functionArgs...)
}
