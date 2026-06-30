// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aerospike provides functions to trace the aerospike/aerospike-client-go package (https://github.com/aerospike/aerospike-client-go).
//
// `WrapClient` will wrap an aerospike `Client` and return a new struct with all
// the same methods, so should be seamless for existing applications. It also
// has an additional `WithContext` method which can be used to connect a span
// to an existing trace.
//
// When using Orchestrion for automatic instrumentation, a method-call aspect
// rewrites every `client.Method(...)` call site to
// `WrapClientWithContext(client, ctx).Method(...)`. The context is resolved
// from the enclosing function's `context.Context` argument (via
// `.Function.ArgumentOfType`); when none is in scope a nil is passed and the
// tracer's GLS-aware `SpanFromContext` provides same-goroutine parenting
// automatically.
//
// GLS is goroutine-local and is not copied across goroutine boundaries, so to
// parent spans created inside a goroutine, pass the context into the goroutine
// as a parameter rather than capturing it, so the aspect can see it in scope:
//
//	go func(ctx context.Context) { client.Put(nil, key, bins) }(ctx)
package aerospike // import "github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2"

import (
	"context"
	"sync"
	"time"

	as "github.com/aerospike/aerospike-client-go/v7"

	"github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2/internal/tracing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// orchestrionCfg caches the default clientConfig so that WrapClientWithContext
// (called at every instrumented call site by Orchestrion) only resolves service
// and operation names from the instrumentation registry once.
var orchestrionCfg = sync.OnceValue(func() *clientConfig {
	cfg := new(clientConfig)
	defaults(cfg)
	return cfg
})

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

// WrapClientWithContext wraps client for a single call using the provided ctx.
// It is called by the Orchestrion-injected advice at each instrumented call
// site. A nil ctx (passed when no context.Context is in scope at the call site)
// falls back to context.Background(); the tracer's GLS then provides
// same-goroutine parenting automatically.
func WrapClientWithContext(client *as.Client, ctx context.Context) *Client {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Client{Client: client, cfg: orchestrionCfg(), context: ctx}
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

// startSpan starts a span from the context set with WithContext.
func (c *Client) startSpan(resourceName string) *tracer.Span {
	return tracing.StartSpan(c.context, c.cfg.serviceName, c.cfg.serviceSource, c.cfg.operationName, resourceName)
}

// wrapped methods:

// Put invokes and traces Client.Put.
func (c *Client) Put(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) as.Error {
	span := c.startSpan("Put")
	err := c.Client.Put(policy, key, binMap)
	span.Finish(tracer.WithError(err))
	return err
}

// PutBins invokes and traces Client.PutBins.
func (c *Client) PutBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) as.Error {
	span := c.startSpan("PutBins")
	err := c.Client.PutBins(policy, key, bins...)
	span.Finish(tracer.WithError(err))
	return err
}

// Append invokes and traces Client.Append.
func (c *Client) Append(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) as.Error {
	span := c.startSpan("Append")
	err := c.Client.Append(policy, key, binMap)
	span.Finish(tracer.WithError(err))
	return err
}

// AppendBins invokes and traces Client.AppendBins.
func (c *Client) AppendBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) as.Error {
	span := c.startSpan("AppendBins")
	err := c.Client.AppendBins(policy, key, bins...)
	span.Finish(tracer.WithError(err))
	return err
}

// Prepend invokes and traces Client.Prepend.
func (c *Client) Prepend(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) as.Error {
	span := c.startSpan("Prepend")
	err := c.Client.Prepend(policy, key, binMap)
	span.Finish(tracer.WithError(err))
	return err
}

// PrependBins invokes and traces Client.PrependBins.
func (c *Client) PrependBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) as.Error {
	span := c.startSpan("PrependBins")
	err := c.Client.PrependBins(policy, key, bins...)
	span.Finish(tracer.WithError(err))
	return err
}

// Add invokes and traces Client.Add.
func (c *Client) Add(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) as.Error {
	span := c.startSpan("Add")
	err := c.Client.Add(policy, key, binMap)
	span.Finish(tracer.WithError(err))
	return err
}

// AddBins invokes and traces Client.AddBins.
func (c *Client) AddBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) as.Error {
	span := c.startSpan("AddBins")
	err := c.Client.AddBins(policy, key, bins...)
	span.Finish(tracer.WithError(err))
	return err
}

// Delete invokes and traces Client.Delete.
func (c *Client) Delete(policy *as.WritePolicy, key *as.Key) (bool, as.Error) {
	span := c.startSpan("Delete")
	existed, err := c.Client.Delete(policy, key)
	span.Finish(tracer.WithError(err))
	return existed, err
}

// Touch invokes and traces Client.Touch.
func (c *Client) Touch(policy *as.WritePolicy, key *as.Key) as.Error {
	span := c.startSpan("Touch")
	err := c.Client.Touch(policy, key)
	span.Finish(tracer.WithError(err))
	return err
}

// Exists invokes and traces Client.Exists.
func (c *Client) Exists(policy *as.BasePolicy, key *as.Key) (bool, as.Error) {
	span := c.startSpan("Exists")
	exists, err := c.Client.Exists(policy, key)
	span.Finish(tracer.WithError(err))
	return exists, err
}

// BatchExists invokes and traces Client.BatchExists.
func (c *Client) BatchExists(policy *as.BatchPolicy, keys []*as.Key) ([]bool, as.Error) {
	span := c.startSpan("BatchExists")
	results, err := c.Client.BatchExists(policy, keys)
	span.Finish(tracer.WithError(err))
	return results, err
}

// Get invokes and traces Client.Get.
func (c *Client) Get(policy *as.BasePolicy, key *as.Key, binNames ...string) (*as.Record, as.Error) {
	span := c.startSpan("Get")
	record, err := c.Client.Get(policy, key, binNames...)
	span.Finish(tracer.WithError(err))
	return record, err
}

// GetHeader invokes and traces Client.GetHeader.
func (c *Client) GetHeader(policy *as.BasePolicy, key *as.Key) (*as.Record, as.Error) {
	span := c.startSpan("GetHeader")
	record, err := c.Client.GetHeader(policy, key)
	span.Finish(tracer.WithError(err))
	return record, err
}

// BatchGet invokes and traces Client.BatchGet.
func (c *Client) BatchGet(policy *as.BatchPolicy, keys []*as.Key, binNames ...string) ([]*as.Record, as.Error) {
	span := c.startSpan("BatchGet")
	records, err := c.Client.BatchGet(policy, keys, binNames...)
	span.Finish(tracer.WithError(err))
	return records, err
}

// BatchGetHeader invokes and traces Client.BatchGetHeader.
func (c *Client) BatchGetHeader(policy *as.BatchPolicy, keys []*as.Key) ([]*as.Record, as.Error) {
	span := c.startSpan("BatchGetHeader")
	records, err := c.Client.BatchGetHeader(policy, keys)
	span.Finish(tracer.WithError(err))
	return records, err
}

// BatchGetOperate invokes and traces Client.BatchGetOperate.
func (c *Client) BatchGetOperate(policy *as.BatchPolicy, keys []*as.Key, ops ...*as.Operation) ([]*as.Record, as.Error) {
	span := c.startSpan("BatchGetOperate")
	records, err := c.Client.BatchGetOperate(policy, keys, ops...)
	span.Finish(tracer.WithError(err))
	return records, err
}

// Operate invokes and traces Client.Operate.
func (c *Client) Operate(policy *as.WritePolicy, key *as.Key, operations ...*as.Operation) (*as.Record, as.Error) {
	span := c.startSpan("Operate")
	record, err := c.Client.Operate(policy, key, operations...)
	span.Finish(tracer.WithError(err))
	return record, err
}

// ScanAll invokes and traces Client.ScanAll.
func (c *Client) ScanAll(apolicy *as.ScanPolicy, namespace string, setName string, binNames ...string) (*as.Recordset, as.Error) {
	span := c.startSpan("ScanAll")
	recordset, err := c.Client.ScanAll(apolicy, namespace, setName, binNames...)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// ScanPartitions invokes and traces Client.ScanPartitions.
func (c *Client) ScanPartitions(apolicy *as.ScanPolicy, partitionFilter *as.PartitionFilter, namespace string, setName string, binNames ...string) (*as.Recordset, as.Error) {
	span := c.startSpan("ScanPartitions")
	recordset, err := c.Client.ScanPartitions(apolicy, partitionFilter, namespace, setName, binNames...)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// BatchGetComplex invokes and traces Client.BatchGetComplex.
func (c *Client) BatchGetComplex(policy *as.BatchPolicy, records []*as.BatchRead) as.Error {
	span := c.startSpan("BatchGetComplex")
	err := c.Client.BatchGetComplex(policy, records)
	span.Finish(tracer.WithError(err))
	return err
}

// BatchDelete invokes and traces Client.BatchDelete.
func (c *Client) BatchDelete(policy *as.BatchPolicy, deletePolicy *as.BatchDeletePolicy, keys []*as.Key) ([]*as.BatchRecord, as.Error) {
	span := c.startSpan("BatchDelete")
	results, err := c.Client.BatchDelete(policy, deletePolicy, keys)
	span.Finish(tracer.WithError(err))
	return results, err
}

// BatchOperate invokes and traces Client.BatchOperate.
func (c *Client) BatchOperate(policy *as.BatchPolicy, records []as.BatchRecordIfc) as.Error {
	span := c.startSpan("BatchOperate")
	err := c.Client.BatchOperate(policy, records)
	span.Finish(tracer.WithError(err))
	return err
}

// BatchExecute invokes and traces Client.BatchExecute.
func (c *Client) BatchExecute(policy *as.BatchPolicy, udfPolicy *as.BatchUDFPolicy, keys []*as.Key, packageName string, functionName string, args ...as.Value) ([]*as.BatchRecord, as.Error) {
	span := c.startSpan("BatchExecute")
	results, err := c.Client.BatchExecute(policy, udfPolicy, keys, packageName, functionName, args...)
	span.Finish(tracer.WithError(err))
	return results, err
}

// Execute invokes and traces Client.Execute.
func (c *Client) Execute(policy *as.WritePolicy, key *as.Key, packageName string, functionName string, args ...as.Value) (any, as.Error) {
	span := c.startSpan("Execute")
	result, err := c.Client.Execute(policy, key, packageName, functionName, args...)
	span.Finish(tracer.WithError(err))
	return result, err
}

// ExecuteUDF invokes and traces Client.ExecuteUDF.
func (c *Client) ExecuteUDF(policy *as.QueryPolicy, statement *as.Statement, packageName string, functionName string, functionArgs ...as.Value) (*as.ExecuteTask, as.Error) {
	span := c.startSpan("ExecuteUDF")
	task, err := c.Client.ExecuteUDF(policy, statement, packageName, functionName, functionArgs...)
	span.Finish(tracer.WithError(err))
	return task, err
}

// ExecuteUDFNode invokes and traces Client.ExecuteUDFNode.
func (c *Client) ExecuteUDFNode(policy *as.QueryPolicy, node *as.Node, statement *as.Statement, packageName string, functionName string, functionArgs ...as.Value) (*as.ExecuteTask, as.Error) {
	span := c.startSpan("ExecuteUDFNode")
	task, err := c.Client.ExecuteUDFNode(policy, node, statement, packageName, functionName, functionArgs...)
	span.Finish(tracer.WithError(err))
	return task, err
}

// QueryExecute invokes and traces Client.QueryExecute.
func (c *Client) QueryExecute(policy *as.QueryPolicy, writePolicy *as.WritePolicy, statement *as.Statement, ops ...*as.Operation) (*as.ExecuteTask, as.Error) {
	span := c.startSpan("QueryExecute")
	task, err := c.Client.QueryExecute(policy, writePolicy, statement, ops...)
	span.Finish(tracer.WithError(err))
	return task, err
}

// QueryPartitions invokes and traces Client.QueryPartitions.
func (c *Client) QueryPartitions(policy *as.QueryPolicy, statement *as.Statement, partitionFilter *as.PartitionFilter) (*as.Recordset, as.Error) {
	span := c.startSpan("QueryPartitions")
	recordset, err := c.Client.QueryPartitions(policy, statement, partitionFilter)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// Query invokes and traces Client.Query.
func (c *Client) Query(policy *as.QueryPolicy, statement *as.Statement) (*as.Recordset, as.Error) {
	span := c.startSpan("Query")
	recordset, err := c.Client.Query(policy, statement)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// QueryNode invokes and traces Client.QueryNode.
func (c *Client) QueryNode(policy *as.QueryPolicy, node *as.Node, statement *as.Statement) (*as.Recordset, as.Error) {
	span := c.startSpan("QueryNode")
	recordset, err := c.Client.QueryNode(policy, node, statement)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// ScanNode invokes and traces Client.ScanNode.
func (c *Client) ScanNode(apolicy *as.ScanPolicy, node *as.Node, namespace string, setName string, binNames ...string) (*as.Recordset, as.Error) {
	span := c.startSpan("ScanNode")
	recordset, err := c.Client.ScanNode(apolicy, node, namespace, setName, binNames...)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// Truncate invokes and traces Client.Truncate.
func (c *Client) Truncate(policy *as.InfoPolicy, namespace, set string, beforeLastUpdate *time.Time) as.Error {
	span := c.startSpan("Truncate")
	err := c.Client.Truncate(policy, namespace, set, beforeLastUpdate)
	span.Finish(tracer.WithError(err))
	return err
}
