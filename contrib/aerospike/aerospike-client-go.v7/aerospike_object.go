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
func (c *Client) PutObject(policy *as.WritePolicy, key *as.Key, obj interface{}) as.Error {
	span := c.startSpan("PutObject")
	err := c.Client.PutObject(policy, key, obj)
	span.Finish(tracer.WithError(err))
	return err
}

// GetObject invokes and traces Client.GetObject.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) GetObject(policy *as.BasePolicy, key *as.Key, obj interface{}) as.Error {
	span := c.startSpan("GetObject")
	err := c.Client.GetObject(policy, key, obj)
	span.Finish(tracer.WithError(err))
	return err
}

// BatchGetObjects invokes and traces Client.BatchGetObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) BatchGetObjects(policy *as.BatchPolicy, keys []*as.Key, objects []interface{}) ([]bool, as.Error) {
	span := c.startSpan("BatchGetObjects")
	found, err := c.Client.BatchGetObjects(policy, keys, objects)
	span.Finish(tracer.WithError(err))
	return found, err
}

// ScanAllObjects invokes and traces Client.ScanAllObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) ScanAllObjects(apolicy *as.ScanPolicy, objChan interface{}, namespace string, setName string, binNames ...string) (*as.Recordset, as.Error) {
	span := c.startSpan("ScanAllObjects")
	recordset, err := c.Client.ScanAllObjects(apolicy, objChan, namespace, setName, binNames...)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// ScanNodeObjects invokes and traces Client.ScanNodeObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) ScanNodeObjects(apolicy *as.ScanPolicy, node *as.Node, objChan interface{}, namespace string, setName string, binNames ...string) (*as.Recordset, as.Error) {
	span := c.startSpan("ScanNodeObjects")
	recordset, err := c.Client.ScanNodeObjects(apolicy, node, objChan, namespace, setName, binNames...)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// ScanPartitionObjects invokes and traces Client.ScanPartitionObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) ScanPartitionObjects(apolicy *as.ScanPolicy, objChan interface{}, partitionFilter *as.PartitionFilter, namespace string, setName string, binNames ...string) (*as.Recordset, as.Error) {
	span := c.startSpan("ScanPartitionObjects")
	recordset, err := c.Client.ScanPartitionObjects(apolicy, objChan, partitionFilter, namespace, setName, binNames...)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// QueryObjects invokes and traces Client.QueryObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) QueryObjects(policy *as.QueryPolicy, statement *as.Statement, objChan interface{}) (*as.Recordset, as.Error) {
	span := c.startSpan("QueryObjects")
	recordset, err := c.Client.QueryObjects(policy, statement, objChan)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// QueryNodeObjects invokes and traces Client.QueryNodeObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) QueryNodeObjects(policy *as.QueryPolicy, node *as.Node, statement *as.Statement, objChan interface{}) (*as.Recordset, as.Error) {
	span := c.startSpan("QueryNodeObjects")
	recordset, err := c.Client.QueryNodeObjects(policy, node, statement, objChan)
	span.Finish(tracer.WithError(err))
	return recordset, err
}

// QueryPartitionObjects invokes and traces Client.QueryPartitionObjects.
// This method is only available when the aerospike library is built without
// the as_performance build tag (which removes the reflection-based Object API).
func (c *Client) QueryPartitionObjects(policy *as.QueryPolicy, statement *as.Statement, objChan interface{}, partitionFilter *as.PartitionFilter) (*as.Recordset, as.Error) {
	span := c.startSpan("QueryPartitionObjects")
	recordset, err := c.Client.QueryPartitionObjects(policy, statement, objChan, partitionFilter)
	span.Finish(tracer.WithError(err))
	return recordset, err
}
