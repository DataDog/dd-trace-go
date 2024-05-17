// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"context"
	"database/sql/driver"
	"io"
)

// MockDriver implements a mock driver that captures and stores prepared and executed statements
type MockDriver struct {
	Prepared []string
	Executed []string
	// Hook is an optional function to run during a DB operation
	Hook func()
}

// Open implements the Conn interface
func (d *MockDriver) Open(_ string) (driver.Conn, error) {
	if d.Hook != nil {
		d.Hook()
	}
	return &mockConn{driver: d}, nil
}

type mockConn struct {
	driver *MockDriver
}

// Prepare implements the driver.Conn interface
func (m *mockConn) Prepare(query string) (driver.Stmt, error) {
	m.driver.Prepared = append(m.driver.Prepared, query)
	if m.driver.Hook != nil {
		m.driver.Hook()
	}
	return &mockStmt{stmt: query, driver: m.driver}, nil
}

// QueryContext implements the QueryerContext interface
func (m *mockConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	m.driver.Executed = append(m.driver.Executed, query)
	if m.driver.Hook != nil {
		m.driver.Hook()
	}
	return &rows{}, nil
}

// ExecContext implements the ExecerContext interface
func (m *mockConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	m.driver.Executed = append(m.driver.Executed, query)
	if m.driver.Hook != nil {
		m.driver.Hook()
	}
	return &mockResult{}, nil
}

// Close implements the Conn interface
func (m *mockConn) Close() (err error) {
	return nil
}

// Begin implements the Conn interface
func (m *mockConn) Begin() (driver.Tx, error) {
	if m.driver.Hook != nil {
		m.driver.Hook()
	}
	return &mockTx{driver: m.driver}, nil
}

type rows struct{}

// Columns implements the Rows interface
func (r *rows) Columns() []string {
	return []string{}
}

// Close implements the Rows interface
func (r *rows) Close() error {
	return nil
}

// Next implements the Rows interface
func (r *rows) Next(_ []driver.Value) error {
	return io.EOF
}

type mockTx struct {
	driver *MockDriver
}

// Commit implements the Tx interface
func (t *mockTx) Commit() error {
	if t.driver.Hook != nil {
		t.driver.Hook()
	}
	return nil
}

// Rollback implements the Tx interface
func (t *mockTx) Rollback() error {
	return nil
}

type mockStmt struct {
	stmt   string
	driver *MockDriver
}

// Close implements the Stmt interface
func (s *mockStmt) Close() error {
	return nil
}

// NumInput implements the Stmt interface
func (s *mockStmt) NumInput() int {
	return 0
}

// Exec implements the Stmt interface
func (s *mockStmt) Exec(_ []driver.Value) (driver.Result, error) {
	s.driver.Executed = append(s.driver.Executed, s.stmt)
	return &mockResult{}, nil
}

// Query implements the Stmt interface
func (s *mockStmt) Query(_ []driver.Value) (driver.Rows, error) {
	s.driver.Executed = append(s.driver.Executed, s.stmt)
	return &rows{}, nil
}

// ExecContext implements the StmtExecContext interface
func (s *mockStmt) ExecContext(_ context.Context, _ []driver.NamedValue) (driver.Result, error) {
	s.driver.Executed = append(s.driver.Executed, s.stmt)
	if s.driver.Hook != nil {
		s.driver.Hook()
	}
	return &mockResult{}, nil
}

// QueryContext implements the StmtQueryContext interface
func (s *mockStmt) QueryContext(_ context.Context, _ []driver.NamedValue) (driver.Rows, error) {
	s.driver.Executed = append(s.driver.Executed, s.stmt)
	if s.driver.Hook != nil {
		s.driver.Hook()
	}
	return &rows{}, nil
}

type mockResult struct{}

// LastInsertId implements the Result interface
func (r *mockResult) LastInsertId() (int64, error) {
	return 0, nil
}

// RowsAffected implements the Result interface
func (r *mockResult) RowsAffected() (int64, error) {
	return 0, nil
}
