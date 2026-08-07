// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !as_performance

package aerospike

import (
	as "github.com/aerospike/aerospike-client-go/v7"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// PutObject invokes and traces Client.PutObject.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) PutObject(policy *as.WritePolicy, key *as.Key, obj interface{}) (err as.Error) {
	span := c.startSpan("PutObject")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.PutObject(policy, key, obj)
}

// GetObject invokes and traces Client.GetObject.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) GetObject(policy *as.BasePolicy, key *as.Key, obj interface{}) (err as.Error) {
	span := c.startSpan("GetObject")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.GetObject(policy, key, obj)
}

// BatchGetObjects invokes and traces Client.BatchGetObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) BatchGetObjects(policy *as.BatchPolicy, keys []*as.Key, objects []interface{}) (found []bool, err as.Error) {
	span := c.startSpan("BatchGetObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.BatchGetObjects(policy, keys, objects)
}

// ScanAllObjects invokes and traces Client.ScanAllObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) ScanAllObjects(apolicy *as.ScanPolicy, objChan interface{}, namespace string, setName string, binNames ...string) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("ScanAllObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanAllObjects(apolicy, objChan, namespace, setName, binNames...)
}

// ScanNodeObjects invokes and traces Client.ScanNodeObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) ScanNodeObjects(apolicy *as.ScanPolicy, node *as.Node, objChan interface{}, namespace string, setName string, binNames ...string) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("ScanNodeObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanNodeObjects(apolicy, node, objChan, namespace, setName, binNames...)
}

// ScanPartitionObjects invokes and traces Client.ScanPartitionObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) ScanPartitionObjects(apolicy *as.ScanPolicy, objChan interface{}, partitionFilter *as.PartitionFilter, namespace string, setName string, binNames ...string) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("ScanPartitionObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.ScanPartitionObjects(apolicy, objChan, partitionFilter, namespace, setName, binNames...)
}

// QueryObjects invokes and traces Client.QueryObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) QueryObjects(policy *as.QueryPolicy, statement *as.Statement, objChan interface{}) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("QueryObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryObjects(policy, statement, objChan)
}

// QueryNodeObjects invokes and traces Client.QueryNodeObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) QueryNodeObjects(policy *as.QueryPolicy, node *as.Node, statement *as.Statement, objChan interface{}) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("QueryNodeObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryNodeObjects(policy, node, statement, objChan)
}

// QueryPartitionObjects invokes and traces Client.QueryPartitionObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) QueryPartitionObjects(policy *as.QueryPolicy, statement *as.Statement, objChan interface{}, partitionFilter *as.PartitionFilter) (recordset *as.Recordset, err as.Error) {
	span := c.startSpan("QueryPartitionObjects")
	defer func() { span.Finish(tracer.WithError(err)) }()
	return c.Client.QueryPartitionObjects(policy, statement, objChan, partitionFilter)
}
