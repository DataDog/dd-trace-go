// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"testing"
	"time"

	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/require"

	aerospiketrace "github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseLibraryCalls covers ScanAll and Query, which call other Client
// methods inside the library. TestExactSpans checks that each call gives one
// span.
type TestCaseLibraryCalls struct {
	client *as.Client
}

func (tc *TestCaseLibraryCalls) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseLibraryCalls) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "exact-key")
	require.NoError(t, err)
	require.NoError(t, tc.client.Put(nil, key, as.BinMap{"value": "hello"}))
	_, err = tc.client.Get(nil, key)
	require.NoError(t, err)

	rs, err := tc.client.ScanAll(nil, "test", "testset")
	require.NoError(t, err)
	rs.Close()

	rs, err = tc.client.Query(nil, as.NewStatement("test", "testset"))
	require.NoError(t, err)
	rs.Close()
}

func (*TestCaseLibraryCalls) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				aerospikeChild("Put"),
				aerospikeChild("Get"),
				aerospikeChild("ScanAll"),
				aerospikeChild("Query"),
			},
		},
	}
}

// TestCaseCallForms covers a spread variadic argument, a single call that
// returns every argument, and a call without a context.Context in scope.
type TestCaseCallForms struct {
	client *as.Client
}

func (tc *TestCaseCallForms) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseCallForms) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "call-forms-key")
	require.NoError(t, err)

	bins := []*as.Bin{as.NewBin("a", 1), as.NewBin("b", 2)}
	require.NoError(t, tc.client.PutBins(nil, key, bins...))
	require.NoError(t, tc.client.Put(putArgs(key)))
	require.NoError(t, deleteWithoutContext(tc.client, key))
}

func putArgs(key *as.Key) (*as.WritePolicy, *as.Key, as.BinMap) {
	return nil, key, as.BinMap{"value": "multi"}
}

// deleteWithoutContext has no context.Context in scope, so the span is
// parented from the goroutine-local active span.
func deleteWithoutContext(client *as.Client, key *as.Key) error {
	_, err := client.Delete(nil, key)
	return err
}

func (*TestCaseCallForms) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				aerospikeChild("PutBins"),
				aerospikeChild("Put"),
				aerospikeChild("Delete"),
			},
		},
	}
}

// TestCaseGoDefer covers calls run with go and defer. The receiver and the
// arguments are evaluated at the statement, and the span covers the call when
// it runs.
type TestCaseGoDefer struct {
	client *as.Client
}

func (tc *TestCaseGoDefer) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseGoDefer) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "go-defer-key")
	require.NoError(t, err)

	go tc.client.Put(nil, key, as.BinMap{"value": "go"})
	waitForKey(t, tc.client, key)
	touchThenDelete(ctx, tc.client, key)
}

func touchThenDelete(ctx context.Context, client *as.Client, key *as.Key) {
	_ = ctx
	defer client.Delete(nil, key)
	_ = client.Touch(nil, key)
}

// waitForKey polls for key through a method value, which neither tool
// rewrites, so the poll gives no span.
func waitForKey(t *testing.T, client *as.Client, key *as.Key) {
	t.Helper()
	exists := client.Exists
	require.Eventually(t, func() bool {
		ok, err := exists(nil, key)
		return err == nil && ok
	}, 10*time.Second, 10*time.Millisecond)
}

func (*TestCaseGoDefer) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				aerospikeChild("Put"),
				aerospikeChild("Touch"),
				aerospikeChild("Delete"),
			},
		},
	}
}

// TestCaseWrappedClient checks that a client the application wraps with the
// contrib gives one span per call, not two.
type TestCaseWrappedClient struct {
	client *aerospiketrace.Client
}

func (tc *TestCaseWrappedClient) Setup(_ context.Context, t *testing.T) {
	tc.client = aerospiketrace.WrapClient(newClient(t))
}

func (tc *TestCaseWrappedClient) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "wrapped-key")
	require.NoError(t, err)
	require.NoError(t, tc.client.WithContext(ctx).Put(nil, key, as.BinMap{"value": "wrapped"}))
}

func (*TestCaseWrappedClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put")},
		},
	}
}
