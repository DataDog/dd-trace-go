// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike

import (
	"time"

	as "github.com/aerospike/aerospike-client-go/v7"
)

// mockAsClient is a test stub that satisfies the asClient interface.
// Every method returns its zero values so tests can focus on span assertions.
type mockAsClient struct{}

func (m *mockAsClient) Add(_ *as.WritePolicy, _ *as.Key, _ as.BinMap) as.Error {
	return nil
}
func (m *mockAsClient) AddBins(_ *as.WritePolicy, _ *as.Key, _ ...*as.Bin) as.Error {
	return nil
}
func (m *mockAsClient) Append(_ *as.WritePolicy, _ *as.Key, _ as.BinMap) as.Error {
	return nil
}
func (m *mockAsClient) AppendBins(_ *as.WritePolicy, _ *as.Key, _ ...*as.Bin) as.Error {
	return nil
}
func (m *mockAsClient) BatchDelete(_ *as.BatchPolicy, _ *as.BatchDeletePolicy, _ []*as.Key) ([]*as.BatchRecord, as.Error) {
	return nil, nil
}
func (m *mockAsClient) BatchExecute(_ *as.BatchPolicy, _ *as.BatchUDFPolicy, _ []*as.Key, _, _ string, _ ...as.Value) ([]*as.BatchRecord, as.Error) {
	return nil, nil
}
func (m *mockAsClient) BatchExists(_ *as.BatchPolicy, _ []*as.Key) ([]bool, as.Error) {
	return nil, nil
}
func (m *mockAsClient) BatchGet(_ *as.BatchPolicy, _ []*as.Key, _ ...string) ([]*as.Record, as.Error) {
	return nil, nil
}
func (m *mockAsClient) BatchGetComplex(_ *as.BatchPolicy, _ []*as.BatchRead) as.Error {
	return nil
}
func (m *mockAsClient) BatchGetHeader(_ *as.BatchPolicy, _ []*as.Key) ([]*as.Record, as.Error) {
	return nil, nil
}
func (m *mockAsClient) BatchGetOperate(_ *as.BatchPolicy, _ []*as.Key, _ ...*as.Operation) ([]*as.Record, as.Error) {
	return nil, nil
}
func (m *mockAsClient) BatchOperate(_ *as.BatchPolicy, _ []as.BatchRecordIfc) as.Error {
	return nil
}
func (m *mockAsClient) Delete(_ *as.WritePolicy, _ *as.Key) (bool, as.Error) {
	return false, nil
}
func (m *mockAsClient) Execute(_ *as.WritePolicy, _ *as.Key, _, _ string, _ ...as.Value) (any, as.Error) {
	return nil, nil
}
func (m *mockAsClient) ExecuteUDF(_ *as.QueryPolicy, _ *as.Statement, _, _ string, _ ...as.Value) (*as.ExecuteTask, as.Error) {
	return nil, nil
}
func (m *mockAsClient) ExecuteUDFNode(_ *as.QueryPolicy, _ *as.Node, _ *as.Statement, _, _ string, _ ...as.Value) (*as.ExecuteTask, as.Error) {
	return nil, nil
}
func (m *mockAsClient) Exists(_ *as.BasePolicy, _ *as.Key) (bool, as.Error) {
	return false, nil
}
func (m *mockAsClient) Get(_ *as.BasePolicy, _ *as.Key, _ ...string) (*as.Record, as.Error) {
	return nil, nil
}
func (m *mockAsClient) GetHeader(_ *as.BasePolicy, _ *as.Key) (*as.Record, as.Error) {
	return nil, nil
}
func (m *mockAsClient) Operate(_ *as.WritePolicy, _ *as.Key, _ ...*as.Operation) (*as.Record, as.Error) {
	return nil, nil
}
func (m *mockAsClient) Prepend(_ *as.WritePolicy, _ *as.Key, _ as.BinMap) as.Error {
	return nil
}
func (m *mockAsClient) PrependBins(_ *as.WritePolicy, _ *as.Key, _ ...*as.Bin) as.Error {
	return nil
}
func (m *mockAsClient) Put(_ *as.WritePolicy, _ *as.Key, _ as.BinMap) as.Error {
	return nil
}
func (m *mockAsClient) PutBins(_ *as.WritePolicy, _ *as.Key, _ ...*as.Bin) as.Error {
	return nil
}
func (m *mockAsClient) Query(_ *as.QueryPolicy, _ *as.Statement) (*as.Recordset, as.Error) {
	return nil, nil
}
func (m *mockAsClient) QueryExecute(_ *as.QueryPolicy, _ *as.WritePolicy, _ *as.Statement, _ ...*as.Operation) (*as.ExecuteTask, as.Error) {
	return nil, nil
}
func (m *mockAsClient) QueryNode(_ *as.QueryPolicy, _ *as.Node, _ *as.Statement) (*as.Recordset, as.Error) {
	return nil, nil
}
func (m *mockAsClient) QueryPartitions(_ *as.QueryPolicy, _ *as.Statement, _ *as.PartitionFilter) (*as.Recordset, as.Error) {
	return nil, nil
}
func (m *mockAsClient) ScanAll(_ *as.ScanPolicy, _, _ string, _ ...string) (*as.Recordset, as.Error) {
	return nil, nil
}
func (m *mockAsClient) ScanNode(_ *as.ScanPolicy, _ *as.Node, _, _ string, _ ...string) (*as.Recordset, as.Error) {
	return nil, nil
}
func (m *mockAsClient) ScanPartitions(_ *as.ScanPolicy, _ *as.PartitionFilter, _, _ string, _ ...string) (*as.Recordset, as.Error) {
	return nil, nil
}
func (m *mockAsClient) Touch(_ *as.WritePolicy, _ *as.Key) as.Error {
	return nil
}
func (m *mockAsClient) Truncate(_ *as.InfoPolicy, _, _ string, _ *time.Time) as.Error {
	return nil
}
