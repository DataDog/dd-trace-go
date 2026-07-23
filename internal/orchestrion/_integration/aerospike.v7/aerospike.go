// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// newClient dials the Aerospike container, retrying until it is ready.
func newClient(t *testing.T) *as.Client {
	t.Helper()
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartAerospikeTestContainer(t)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	var client *as.Client
	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = 2 * time.Minute
	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				client, aerr = as.NewClient(host, port)
				return aerr
			},
			bo,
		),
	)
	t.Cleanup(func() { client.Close() })
	return client
}

func aerospikeChild(resource string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "aerospike.command",
			"service":  "aerospike",
			"resource": resource,
			"type":     "aerospike",
		},
		Meta: map[string]string{
			"component": "aerospike/aerospike-client-go.v7",
			"span.kind": "client",
			"db.system": "aerospike",
		},
	}
}

// TestCase exercises the common pattern where a context.Context is in scope on
// the function performing the Aerospike calls. Orchestrion rewrites each call
// to propagate ctx, so the Put and Get spans are children of test.root.
type TestCase struct {
	client *as.Client
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "orchestrion-key")
	require.NoError(t, err)

	err = tc.client.Put(nil, key, as.BinMap{"value": "hello"})
	require.NoError(t, err)

	record, err := tc.client.Get(nil, key)
	require.NoError(t, err)
	require.NotNil(t, record)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put"), aerospikeChild("Get")},
		},
	}
}

// TestCaseGoroutine verifies realistic cross-goroutine context propagation
// using only the normal aerospike API. A span is started, and its context is
// passed to a worker function that runs on a separate goroutine and performs a
// Put. Because the worker receives ctx as an argument, Orchestrion's
// method-call rewrite threads it into the span, so the Put is parented under
// test.root even though it runs on a different goroutine — where the tracer's
// goroutine-local storage alone could not recover the parent.
type TestCaseGoroutine struct {
	client *as.Client
}

func (tc *TestCaseGoroutine) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

// putRecord runs on its own goroutine and receives ctx as a parameter. The
// client.Put call below is what Orchestrion rewrites to propagate ctx.
func putRecord(ctx context.Context, client *as.Client, key *as.Key) as.Error {
	// ctx is consumed by the Orchestrion-rewritten call below; the blank
	// assignment keeps the source compiling cleanly without Orchestrion too.
	_ = ctx
	return client.Put(nil, key, as.BinMap{"value": "hello"})
}

func (tc *TestCaseGoroutine) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "orchestrion-goroutine-key")
	require.NoError(t, err)

	errCh := make(chan as.Error, 1)
	go func() { errCh <- putRecord(ctx, tc.client, key) }()
	require.NoError(t, <-errCh)
}

func (tc *TestCaseGoroutine) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put")},
		},
	}
}

// TestCaseScanAll verifies that a direct ScanAll call produces exactly one span
// named "ScanAll". ScanAll delegates internally to ScanPartitions, but that
// delegation happens on the raw *as.Client that the wrapper calls into, which
// Orchestrion does not instrument — so only the outer "ScanAll" span appears.
type TestCaseScanAll struct {
	client *as.Client
}

func (tc *TestCaseScanAll) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseScanAll) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	rs, err := tc.client.ScanAll(nil, "test", "testset")
	require.NoError(t, err)
	if rs != nil {
		rs.Close()
	}
}

func (tc *TestCaseScanAll) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("ScanAll")},
		},
	}
}

// TestCaseQuery verifies that a direct Query call produces exactly one span
// named "Query". Query delegates internally to QueryPartitions on the raw
// *as.Client, which is not instrumented, so only the outer "Query" span appears.
type TestCaseQuery struct {
	client *as.Client
}

func (tc *TestCaseQuery) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseQuery) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	stmt := as.NewStatement("test", "testset")
	rs, err := tc.client.Query(nil, stmt)
	require.NoError(t, err)
	if rs != nil {
		rs.Close()
	}
}

func (tc *TestCaseQuery) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Query")},
		},
	}
}
