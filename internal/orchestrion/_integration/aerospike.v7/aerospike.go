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

// TestCase covers the common case: a context in scope on the calling function,
// so Orchestrion parents the Put and Get spans under test.root.
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

// TestCaseGoroutine verifies context propagation when the aerospike call happens
// on a goroutine that received a context.Context as a parameter. This
// distinguishes call-site context injection (which works across goroutines) from
// GLS-based propagation (which does not cross goroutine boundaries): the parent
// span is started on the test goroutine, so it is absent from the spawned
// goroutine's GLS, and correct parenting can only come from the injected context.
type TestCaseGoroutine struct {
	client *as.Client
}

func (tc *TestCaseGoroutine) Setup(_ context.Context, t *testing.T) {
	tc.client = newClient(t)
}

func (tc *TestCaseGoroutine) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "orchestrion-goroutine-key")
	require.NoError(t, err)

	errCh := make(chan as.Error, 1)
	go putInGoroutine(ctx, tc.client, key, errCh)
	require.NoError(t, <-errCh)
}

// putInGoroutine calls client.Put with a context.Context in scope so the
// instrumentation threads it into the span. It runs on a goroutine that does not
// hold the parent span in its GLS, so correct parenting proves the context
// propagated through the surrounding scope rather than GLS. The error goes back
// to the test goroutine, since require must not call FailNow from here.
func putInGoroutine(ctx context.Context, client *as.Client, key *as.Key, errCh chan<- as.Error) {
	_ = ctx
	errCh <- client.Put(nil, key, as.BinMap{"value": "hello"})
}

func (tc *TestCaseGoroutine) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put")},
		},
	}
}

// TestCaseScanAll checks that ScanAll yields exactly one span: its internal
// delegation to ScanPartitions runs on the raw client, which is not instrumented.
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

// TestCaseQuery checks that Query yields exactly one span: its internal
// delegation to QueryPartitions runs on the raw client, which is not instrumented.
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
