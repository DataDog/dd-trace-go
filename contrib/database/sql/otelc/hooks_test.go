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
	earlyOpens = 0
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

// TestOpenKeepsDSNWhenInitRunsDuringOpen runs init at each point of a
// database/sql.Open that started before it. The connector must only be traced
// once AfterOpen has given it the DSN.
func TestOpenKeepsDSNWhenInitRunsDuringOpen(t *testing.T) {
	const dsn = "file::memory:"
	for _, initAt := range []string{"before OpenDB", "during OpenDB", "after OpenDB"} {
		t.Run(initAt, func(t *testing.T) {
			beforeInit(t)
			openCtx := &testHookContext{}
			BeforeOpen(openCtx, "otelc-hooks-test-driver", dsn)
			if initAt == "before OpenDB" {
				setReady()
			}

			ictx := &testHookContext{params: []any{testConnector{}}}
			BeforeOpenDB(ictx, testConnector{})
			ec, ok := ictx.params[0].(*earlyConnector)
			require.True(t, ok, "an Open that started before init must get an earlyConnector")
			db := sql.OpenDB(ec)
			if initAt == "during OpenDB" {
				setReady()
			}
			AfterOpenDB(ictx, db)
			if initAt == "after OpenDB" {
				setReady()
			}

			assert.Nil(t, ec.traced.Load(), "the connector must wait for its DSN")
			AfterOpen(openCtx, db, nil)
			assert.Equal(t, dsn, ec.dsn)
			requireTraced(t, ec)
			assert.Empty(t, early)
			require.NoError(t, db.Close())
		})
	}
}

// TestOpenDBWaitsForEarlyOpen checks that an OpenDB called while an early Open
// is still running is traced once that Open returns.
func TestOpenDBWaitsForEarlyOpen(t *testing.T) {
	beforeInit(t)
	openCtx := &testHookContext{}
	BeforeOpen(openCtx, "otelc-hooks-test-driver", "file::memory:")
	setReady()

	db, ec := openDBBeforeInit(t, testConnector{})
	assert.Nil(t, ec.traced.Load())
	AfterOpen(openCtx, nil, errors.New("open failed"))
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

// TestSetReadyDoesNotDeadlock runs setReady with a queued driver and an early
// connector. setReady calls the contrib while it holds mu, and the hooks take
// mu, so the contrib calling database/sql.Register, Open or OpenDB would hang.
func TestSetReadyDoesNotDeadlock(t *testing.T) {
	beforeInit(t)
	AfterRegister(&testHookContext{params: []any{"otelc-hooks-test-deadlock-driver", testDriver{}}})
	db, ec := openDBBeforeInit(t, testConnector{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		setReady()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("setReady deadlocked")
	}
	requireTraced(t, ec)
	require.NoError(t, db.Close())
}
