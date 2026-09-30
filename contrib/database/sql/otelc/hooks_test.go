// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otelc/pkg/hook"

	sqltrace "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
)

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

type testConnector struct{}

func (testConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (testConnector) Driver() driver.Driver { return testDriver{} }

// countingConnector opens connections that count how many times they close.
type countingConnector struct {
	closed *atomic.Int32
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	return testConn{c.closed}, nil
}

func (countingConnector) Driver() driver.Driver { return testDriver{} }

type testConn struct {
	closed *atomic.Int32
}

func (testConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (testConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

func (c testConn) Close() error {
	c.closed.Add(1)
	return nil
}

// closingConnector calls close from its Close method.
type closingConnector struct {
	testConnector
	close func() error
}

func (c closingConnector) Close() error { return c.close() }

// TestContribLinks fails to link if a go:linkname declaration in hooks.go no
// longer matches the contrib function it points at.
func TestContribLinks(t *testing.T) {
	traced, wrapped := wrapConnector(testConnector{}, "")
	require.True(t, wrapped)
	_, wrapped = wrapConnector(traced, "")
	assert.False(t, wrapped, "a traced connector must not be wrapped twice")

	db := sql.OpenDB(traced)
	startDBStats(traced, db)
	require.NoError(t, db.Close())

	const name = "otelc-hooks-test-driver"
	assert.False(t, isRegistered(name))
	sqltrace.Register(name, testDriver{})
	assert.True(t, isRegistered(name))
}

// testHookContext implements the parts of hook.HookContext the hooks use.
type testHookContext struct {
	hook.HookContext
	params []any
	data   any
}

func (c *testHookContext) GetParam(i int) any    { return c.params[i] }
func (c *testHookContext) SetParam(i int, v any) { c.params[i] = v }
func (c *testHookContext) SetData(v any)         { c.data = v }
func (c *testHookContext) GetData() any          { return c.data }

// beforeInit makes the hooks behave as they do before init, until the test
// calls setReady.
func beforeInit(t *testing.T) {
	mu.Lock()
	ready = false
	mu.Unlock()
	t.Cleanup(func() {
		if !isReady() {
			setReady()
		}
	})
}

// openDBBeforeInit runs database/sql.OpenDB between the OpenDB hooks, as the
// otelc trampoline does.
func openDBBeforeInit(t *testing.T, c driver.Connector) (*sql.DB, *earlyConnector) {
	ictx := &testHookContext{params: []any{c}}
	BeforeOpenDB(ictx, c)
	ec, ok := ictx.params[0].(*earlyConnector)
	require.True(t, ok, "OpenDB before init must get an earlyConnector")
	db := sql.OpenDB(ec)
	AfterOpenDB(ictx, db)
	return db, ec
}

func requireTraced(t *testing.T, ec *earlyConnector) {
	t.Helper()
	p := ec.traced.Load()
	require.NotNil(t, p, "setReady must trace the early connector")
	_, wrapped := wrapConnector(*p, "")
	require.False(t, wrapped, "the early connector must use a traced connector")
}

func TestOpenDBBeforeInit(t *testing.T) {
	beforeInit(t)
	db, ec := openDBBeforeInit(t, testConnector{})
	assert.Nil(t, ec.traced.Load(), "the contrib must not be called before init")

	setReady()
	requireTraced(t, ec)
	assert.Empty(t, early)
	require.NoError(t, db.Close())
}

func TestOpenBeforeInitKeepsDSN(t *testing.T) {
	beforeInit(t)
	const dsn = "file::memory:"
	openCtx := &testHookContext{}
	BeforeOpen(openCtx, "otelc-hooks-test-driver", dsn)
	// database/sql.Open calls OpenDB, which reaches the OpenDB hooks.
	db, ec := openDBBeforeInit(t, testConnector{})
	AfterOpen(openCtx, db, nil)
	assert.Equal(t, dsn, ec.dsn)

	setReady()
	requireTraced(t, ec)
	require.NoError(t, db.Close())
}

func TestCloseBeforeInit(t *testing.T) {
	beforeInit(t)
	db, ec := openDBBeforeInit(t, testConnector{})
	require.NoError(t, db.Close())

	setReady()
	assert.Nil(t, ec.traced.Load(), "a closed database must not be traced")
}

func TestRegisterWhileInitRuns(t *testing.T) {
	beforeInit(t)
	const name = "otelc-hooks-test-concurrent-driver"
	done := make(chan struct{})
	go func() {
		defer close(done)
		AfterRegister(&testHookContext{params: []any{name, testDriver{}}})
	}()
	setReady()
	<-done
	assert.True(t, isRegistered(name), "the driver must be registered whether it was queued or not")
}

func TestCloseEarlyDBIdle(t *testing.T) {
	beforeInit(t)
	var closed atomic.Int32
	db, ec := openDBBeforeInit(t, countingConnector{&closed})
	require.NoError(t, db.Ping())
	require.Equal(t, 1, db.Stats().Idle)

	setReady()
	requireTraced(t, ec)
	assert.Equal(t, int32(1), closed.Load(), "the connection from before init must be closed")
	assert.Equal(t, 0, db.Stats().Idle)
	require.NoError(t, db.Close())
}

func TestCloseConnectorClosingAnotherDB(t *testing.T) {
	beforeInit(t)
	other, _ := openDBBeforeInit(t, testConnector{})
	db, _ := openDBBeforeInit(t, closingConnector{close: other.Close})

	done := make(chan error, 1)
	go func() { done <- db.Close() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close deadlocked")
	}
}
