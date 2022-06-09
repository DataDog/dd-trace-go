// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/sqltest"

import (
	"context"
	"database/sql/driver"
	"io"
)

type MockDriver struct {
	PreparedStmts   []string
	ExecutedQueries []string
}

func NewMockDriver() (d *MockDriver) {
	return &MockDriver{}
}

func (d *MockDriver) Open(name string) (driver.Conn, error) {
	return &MockConn{driver: d}, nil
}

func (d *MockDriver) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	d.ExecutedQueries = append(d.ExecutedQueries, query)
	return &rows{}, nil
}

func (d *MockDriver) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	d.PreparedStmts = append(d.PreparedStmts, query)
	return &MockStmt{stmt: query, driver: d}, nil
}

func (d *MockDriver) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return &MockTx{driver: d}, nil
}

type MockConn struct {
	driver *MockDriver
}

// Prepare returns a prepared statement, bound to this connection.
func (m *MockConn) Prepare(query string) (driver.Stmt, error) {
	m.driver.PreparedStmts = append(m.driver.PreparedStmts, query)
	return &MockStmt{stmt: query, driver: m.driver}, nil
}

func (m *MockConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	m.driver.ExecutedQueries = append(m.driver.ExecutedQueries, query)
	return &rows{}, nil
}

func (m *MockConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	m.driver.ExecutedQueries = append(m.driver.ExecutedQueries, query)
	return &mockResult{}, nil
}

func (m *MockConn) Close() (err error) {
	return nil
}

func (m *MockConn) Begin() (driver.Tx, error) {
	return &MockTx{driver: m.driver}, nil
}

type rows struct {
}

func (r *rows) Columns() []string {
	return []string{}
}

func (r *rows) Close() error {
	return nil
}

func (r *rows) Next(dest []driver.Value) error {
	return io.EOF
}

type MockTx struct {
	driver *MockDriver
}

func (t *MockTx) Commit() error {
	return nil
}

func (t *MockTx) Rollback() error {
	return nil
}

type MockStmt struct {
	stmt   string
	driver *MockDriver
}

func (s *MockStmt) Close() error {
	return nil
}

func (s *MockStmt) NumInput() int {
	return 0
}

func (s *MockStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.driver.ExecutedQueries = append(s.driver.ExecutedQueries, s.stmt)
	return &mockResult{}, nil
}

func (s *MockStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.driver.ExecutedQueries = append(s.driver.ExecutedQueries, s.stmt)
	return &rows{}, nil
}

func (s *MockStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.driver.ExecutedQueries = append(s.driver.ExecutedQueries, s.stmt)
	return &mockResult{}, nil
}

func (s *MockStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.driver.ExecutedQueries = append(s.driver.ExecutedQueries, s.stmt)
	return &rows{}, nil
}

type mockResult struct {
}

func (r *mockResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r *mockResult) RowsAffected() (int64, error) {
	return 0, nil
}
