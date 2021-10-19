// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aerospike provides functions to trace the aerospike/aerospike-client-go package (https://github.com/aerospike/aerospike-client-go).
//
// `WrapClient` will wrap an aerospike `Client` and return a new struct with all
// the same methods, making it seamless for existing applications. It also
// has an additional `WithContext` method which can be used to connect a span
// to an existing trace.
package aerospike // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/aerospike/aerospike-client.v5"

import (
	"context"
	"math"
	"time"

	as "github.com/aerospike/aerospike-client-go/v5"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// WrapClient wraps an aerospike.Client so that all requests are traced using the
// default tracer with the service name "aerospike".
func WrapClient(client *as.Client, opts ...ClientOption) *Client {
	cfg := new(clientConfig)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/aerospike/aerospike-client.v5: Wrapping Client: %#v", cfg)
	return &Client{
		Client:  client,
		cfg:     cfg,
		context: context.Background(),
	}
}

// A Client is used to trace requests to the aerospike server.
type Client struct {
	// Client specifies the original client which was wrapped. Do not use this client directly.
	// It will result in untraced commands.
	*as.Client
	cfg     *clientConfig
	context context.Context
}

// WithContext creates a copy of the Client with the given context.
func (c *Client) WithContext(ctx context.Context) *Client {
	// the existing aerospike client doesn't support context, but may in the
	// future, so we do a runtime check to detect this
	mc := c.Client
	if wc, ok := (interface{})(c.Client).(interface {
		WithContext(context.Context) *as.Client
	}); ok {
		mc = wc.WithContext(ctx)
	}
	return &Client{
		Client:  mc,
		cfg:     c.cfg,
		context: ctx,
	}
}

// startSpan starts a span from the context set with WithContext.
func (c *Client) startSpan(resourceName string) ddtrace.Span {
	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeMemcached),
		tracer.ServiceName(c.cfg.serviceName),
		tracer.ResourceName(resourceName),
	}
	if !math.IsNaN(c.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, c.cfg.analyticsRate))
	}
	span, _ := tracer.StartSpanFromContext(c.context, defaultOp, opts...)
	return span
}

// wrapped methods:

// Put writes record bin(s) to the server.
// The policy specifies the transaction timeout, record expiration and how the transaction is
// handled when the record already exists.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Put(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Put")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Put(policy, key, binMap)
}

// PutBins writes record bin(s) to the server.
// The policy specifies the transaction timeout, record expiration and how the transaction is
// handled when the record already exists.
// This method avoids using the as.BinMap allocation and iteration and is lighter on GC.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) PutBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("PutBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.PutBins(policy, key, bins...)
}

// Append appends bin value's string to existing record bin values.
// The policy specifies the transaction timeout, record expiration and how the transaction is
// handled when the record already exists.
// This call only works for string and []byte values.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Append(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Append")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Append(policy, key, binMap)
}

// AppendBins works the same as Append, but avoids as.BinMap allocation and iteration.
func (c *Client) AppendBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("AppendBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.AppendBins(policy, key, bins...)
}

// Prepend prepends bin value's string to existing record bin values.
// The policy specifies the transaction timeout, record expiration and how the transaction is
// handled when the record already exists.
// This call works only for string and []byte values.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Prepend(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Prepend")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Prepend(policy, key, binMap)
}

// PrependBins works the same as Prepend, but avoids as.BinMap allocation and iteration.
func (c *Client) PrependBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("PrependBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.PrependBins(policy, key, bins...)
}

// Add adds integer bin values to existing record bin values.
// The policy specifies the transaction timeout, record expiration and how the transaction is
// handled when the record already exists.
// This call only works for integer values.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Add(policy *as.WritePolicy, key *as.Key, binMap as.BinMap) (err as.Error) {
	span := c.startSpan("Add")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Add(policy, key, binMap)
}

// AddBins works the same as Add, but avoids as.BinMap allocation and iteration.
func (c *Client) AddBins(policy *as.WritePolicy, key *as.Key, bins ...*as.Bin) (err as.Error) {
	span := c.startSpan("AddBins")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.AddBins(policy, key, bins...)
}

// Delete deletes a record for specified key.
// The policy specifies the transaction timeout.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Delete(policy *as.WritePolicy, key *as.Key) (existed bool, err as.Error) {
	span := c.startSpan("Delete")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Delete(policy, key)
}

// Touch updates a record's metadata.
// If the record exists, the record's TTL will be reset to the
// policy's expiration.
// If the record doesn't exist, it will return an error.
func (c *Client) Touch(policy *as.WritePolicy, key *as.Key) (err as.Error) {
	span := c.startSpan("Touch")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Touch(policy, key)
}

// Exists determine if a record key exists.
// The policy can be used to specify timeouts.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Exists(policy *as.BasePolicy, key *as.Key) (exist bool, err as.Error) {
	span := c.startSpan("Exists")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Exists(policy, key)
}

// BatchExists determines if multiple record keys exist in one batch request.
// The returned boolean array is in positional order with the original key array order.
// The policy can be used to specify timeouts.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) BatchExists(policy *as.BatchPolicy, keys []*as.Key) (existsArray []bool, err as.Error) {
	span := c.startSpan("BatchExists")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchExists(policy, keys)
}

// Get reads a record header and bins for specified key.
// The policy can be used to specify timeouts.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Get(policy *as.BasePolicy, key *as.Key, binNames ...string) (record *as.Record, err as.Error) {
	span := c.startSpan("Get")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Get(policy, key, binNames...)
}

// GetHeader reads a record generation and expiration only for specified key.
// Bins are not read.
// The policy can be used to specify timeouts.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) GetHeader(policy *as.BasePolicy, key *as.Key) (record *as.Record, err as.Error) {
	span := c.startSpan("GetHeader")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.GetHeader(policy, key)
}

// BatchGet reads multiple record headers and bins for specified keys in one batch request.
// The returned records are in positional order with the original key array order.
// If a key is not found, the positional record will be nil.
// The policy can be used to specify timeouts.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) BatchGet(policy *as.BatchPolicy, keys []*as.Key, binNames ...string) (records []*as.Record, err as.Error) {
	span := c.startSpan("BatchGet")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGet(policy, keys, binNames...)
}

// BatchGetOperate reads multiple records for specified keys using read operations in one batch call.
// The returned records are in positional order with the original key array order.
// If a key is not found, the positional record will be null.
//
// If a batch request to a node fails, the entire batch is cancelled.
func (c *Client) BatchGetOperate(policy *as.BatchPolicy, keys []*as.Key, ops ...*as.Operation) (records []*as.Record, err as.Error) {
	span := c.startSpan("BatchGetOperate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetOperate(policy, keys, ops...)
}

// BatchGetComplex reads multiple records for specified batch keys in one batch call.
// This method allows different namespaces/bins to be requested for each key in the batch.
// The returned records are located in the same list.
// If the BatchRead key field is not found, the corresponding record field will be null.
// The policy can be used to specify timeouts and maximum concurrent threads.
// This method requires Aerospike Server version >= 3.6.0.
func (c *Client) BatchGetComplex(policy *as.BatchPolicy, records []*as.BatchRead) (err as.Error) {
	span := c.startSpan("BatchGetComplex")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetComplex(policy, records)
}

// BatchGetHeader reads multiple record header data for specified keys in one batch request.
// The returned records are in positional order with the original key array order.
// If a key is not found, the positional record will be nil.
// The policy can be used to specify timeouts.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) BatchGetHeader(policy *as.BatchPolicy, keys []*as.Key) (records []*as.Record, err as.Error) {
	span := c.startSpan("BatchGetHeader")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetHeader(policy, keys)
}

// Operate performs multiple read/write operations on a single key in one batch request.
// An example would be to add an integer value to an existing record and then
// read the result, all in one database call.
//
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Operate(policy *as.WritePolicy, key *as.Key, operations ...*as.Operation) (record *as.Record, err as.Error) {
	span := c.startSpan("Operate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Operate(policy, key, operations...)
}

// ScanPartitions Read records in specified namespace, set and partition filter.
// If the policy's concurrentNodes is specified, each server node will be read in
// parallel. Otherwise, server nodes are read sequentially.
// If partitionFilter is nil, all partitions will be scanned.
// If the policy is nil, the default relevant policy will be used.
// This method is only supported by Aerospike 4.9+ servers.
func (c *Client) ScanPartitions(apolicy *as.ScanPolicy, partitionFilter *as.PartitionFilter, namespace string, setName string, binNames ...string) (res *as.Recordset, err as.Error) {
	span := c.startSpan("ScanPartitions")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanPartitions(apolicy, partitionFilter, namespace, setName, binNames...)
}

// ScanAll reads all records in specified namespace and set from all nodes.
// If the policy's concurrentNodes is specified, each server node will be read in
// parallel. Otherwise, server nodes are read sequentially.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) ScanAll(apolicy *as.ScanPolicy, namespace string, setName string, binNames ...string) (res *as.Recordset, err as.Error) {
	span := c.startSpan("ScanAll")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanAll(apolicy, namespace, setName, binNames...)
}

// ScanNode reads all records in specified namespace and set for one node only.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) ScanNode(apolicy *as.ScanPolicy, node *as.Node, namespace string, setName string, binNames ...string) (res *as.Recordset, err as.Error) {
	span := c.startSpan("ScanNode")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanNode(apolicy, node, namespace, setName, binNames...)
}

// Execute executes a user defined function on server and return results.
// The function operates on a single record.
// The package name is used to locate the udf file location:
//
// udf file = <server udf dir>/<package name>.lua
//
// This method is only supported by Aerospike 3+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Execute(policy *as.WritePolicy, key *as.Key, packageName string, functionName string, args ...as.Value) (res interface{}, err as.Error) {
	span := c.startSpan("Execute")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Execute(policy, key, packageName, functionName, args...)
}

// QueryExecute applies operations on records that match the statement filter.
// Records are not returned to the client.
// This asynchronous server call will return before the command is complete.
// The user can optionally wait for command completion by using the returned
// ExecuteTask instance.
//
// This method is only supported by Aerospike 3+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) QueryExecute(policy *as.QueryPolicy,
	writePolicy *as.WritePolicy,
	statement *as.Statement,
	ops ...*as.Operation,
) (res *as.ExecuteTask, err as.Error) {
	span := c.startSpan("QueryExecute")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryExecute(policy, writePolicy, statement, ops...)
}

// ExecuteUDF applies user defined function on records that match the statement filter.
// Records are not returned to the client.
// This asynchronous server call will return before command is complete.
// The user can optionally wait for command completion by using the returned
// ExecuteTask instance.
//
// This method is only supported by Aerospike 3+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) ExecuteUDF(policy *as.QueryPolicy,
	statement *as.Statement,
	packageName string,
	functionName string,
	functionArgs ...as.Value,
) (task *as.ExecuteTask, err as.Error) {
	span := c.startSpan("ExecuteUDF")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ExecuteUDF(policy, statement, packageName, functionName, functionArgs...)
}

// ExecuteUDFNode applies user defined function on records that match the statement filter on the specified node.
// Records are not returned to the client.
// This asynchronous server call will return before command is complete.
// The user can optionally wait for command completion by using the returned
// ExecuteTask instance.
//
// This method is only supported by Aerospike 3+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) ExecuteUDFNode(policy *as.QueryPolicy,
	node *as.Node,
	statement *as.Statement,
	packageName string,
	functionName string,
	functionArgs ...as.Value,
) (task *as.ExecuteTask, err as.Error) {
	span := c.startSpan("ExecuteUDFNode")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ExecuteUDFNode(policy, node, statement, packageName, functionName, functionArgs...)
}

// QueryPartitions executes a query for specified partitions and returns a recordset.
// The query executor puts records on the channel from separate goroutines.
// The caller can concurrently pop records off the channel through the
// Recordset.Records channel.
//
// This method is only supported by Aerospike 4.9+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) QueryPartitions(policy *as.QueryPolicy, statement *as.Statement, partitionFilter *as.PartitionFilter) (set *as.Recordset, err as.Error) {
	span := c.startSpan("QueryPartitions")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryPartitions(policy, statement, partitionFilter)
}

// Query executes a query and returns a Recordset.
// The query executor puts records on the channel from separate goroutines.
// The caller can concurrently pop records off the channel through the
// Recordset.Records channel.
//
// This method is only supported by Aerospike 3+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) Query(policy *as.QueryPolicy, statement *as.Statement) (set *as.Recordset, err as.Error) {
	span := c.startSpan("Query")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Query(policy, statement)
}

// QueryNode executes a query on a specific node and returns a recordset.
// The caller can concurrently pop records off the channel through the
// record channel.
//
// This method is only supported by Aerospike 3+ servers.
// If the policy is nil, the default relevant policy will be used.
func (c *Client) QueryNode(policy *as.QueryPolicy, node *as.Node, statement *as.Statement) (set *as.Recordset, err as.Error) {
	span := c.startSpan("QueryNode")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryNode(policy, node, statement)
}

// Truncate removes records in specified namespace/set efficiently.  This method is many orders of magnitude
// faster than deleting records one at a time.  Works with Aerospike Server versions >= 3.12.
// This asynchronous server call may return before the truncation is complete.  The user can still
// write new records after the server call returns because new records will have last update times
// greater than the truncate cutoff (set at the time of truncate call).
// For more information, See https://www.aerospike.com/docs/reference/info#truncate
func (c *Client) Truncate(policy *as.WritePolicy, namespace, set string, beforeLastUpdate *time.Time) (err as.Error) {
	span := c.startSpan("Truncate")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.Truncate(policy, namespace, set, beforeLastUpdate)
}
