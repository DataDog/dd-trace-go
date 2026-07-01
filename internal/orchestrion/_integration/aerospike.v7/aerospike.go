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
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	client *as.Client
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartAerospikeTestContainer(t)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = 2 * time.Minute
	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				tc.client, aerr = newClientReturn(host, port)
				return aerr
			},
			bo,
		),
	)
	t.Cleanup(func() { tc.client.Close() })
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

// These helpers type-check constructor shapes that must continue returning
// *as.Client after Orchestrion instrumentation.
func newClientReturn(host string, port int) (*as.Client, as.Error) {
	return as.NewClient(host, port)
}

func newClientAssign(host string, port int) (*as.Client, as.Error) {
	var client *as.Client
	var err as.Error
	client, err = as.NewClient(host, port)
	return client, err
}

func newClientArgument(host string, port int) (*as.Client, as.Error) {
	return acceptClient(as.NewClient(host, port))
}

func acceptClient(client *as.Client, err as.Error) (*as.Client, as.Error) {
	return client, err
}

var (
	_ = newClientAssign
	_ = newClientArgument
)

// withContexter is satisfied by *as.Client when built with Orchestrion, which
// injects a WithContext method via the struct-definition aspect. The interface
// lets the test compile and run without Orchestrion (the assertion simply
// fails and the goroutine falls back to unparented spans).
type withContexter interface {
	WithContext(ctx context.Context) *as.Client
}

// applyCtx calls client.WithContext(ctx) when the injected method is present
// (Orchestrion build), storing ctx in the goroutine-local map so the next
// instrumented method call on this goroutine creates a child span. Without
// Orchestrion the client is returned unchanged.
func applyCtx(client *as.Client, ctx context.Context) *as.Client {
	if wc, ok := any(client).(withContexter); ok {
		return wc.WithContext(ctx)
	}
	return client
}

// TestCaseConcurrent verifies that concurrent calls on a shared *as.Client
// are each instrumented with a span that is correctly parented under test.root.
// Each goroutine calls a different operation (Put / Get / Delete) on its own
// key, making the three expected child spans uniquely matchable by resource name.
type TestCaseConcurrent struct {
	client *as.Client
	putKey *as.Key // written by goroutine 0
	getKey *as.Key // pre-populated in Setup; read by goroutine 1
	delKey *as.Key // pre-populated in Setup; deleted by goroutine 2
}

func (tc *TestCaseConcurrent) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartAerospikeTestContainer(t)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	bo2 := backoff.NewExponentialBackOff()
	bo2.MaxElapsedTime = 2 * time.Minute
	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				tc.client, aerr = newClientReturn(host, port)
				return aerr
			},
			bo2,
		),
	)
	t.Cleanup(func() { tc.client.Close() })

	tc.putKey, err = as.NewKey("test", "testset", "concurrent-put")
	require.NoError(t, err)
	tc.getKey, err = as.NewKey("test", "testset", "concurrent-get")
	require.NoError(t, err)
	tc.delKey, err = as.NewKey("test", "testset", "concurrent-del")
	require.NoError(t, err)

	// Pre-populate getKey and delKey before the tracer starts so these Puts
	// don't generate spans of their own.
	require.NoError(t, tc.client.Put(nil, tc.getKey, as.BinMap{"value": "get"}))
	require.NoError(t, tc.client.Put(nil, tc.delKey, as.BinMap{"value": "del"}))
}

func (tc *TestCaseConcurrent) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	type result struct{ err as.Error }
	results := make([]result, 3)
	var wg sync.WaitGroup
	wg.Add(3)

	// applyCtx(tc.client, ctx) calls client.WithContext(ctx) when Orchestrion
	// has injected it, storing ctx in the per-goroutine map so the method call
	// that follows creates a child span of test.root.
	go func() {
		defer wg.Done()
		results[0].err = applyCtx(tc.client, ctx).Put(nil, tc.putKey, as.BinMap{"value": "put"})
	}()
	go func() { defer wg.Done(); _, results[1].err = applyCtx(tc.client, ctx).Get(nil, tc.getKey) }()
	go func() { defer wg.Done(); _, results[2].err = applyCtx(tc.client, ctx).Delete(nil, tc.delKey) }()

	wg.Wait()
	for _, r := range results {
		require.NoError(t, r.err)
	}
}

func (tc *TestCaseConcurrent) ExpectedTraces() trace.Traces {
	child := func(resource string) *trace.Trace {
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
	// Each goroutine uses a different operation so the three child spans are
	// uniquely matchable by resource name — no two expected entries can resolve
	// to the same actual span.
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				child("Put"),
				child("Get"),
				child("Delete"),
			},
		},
	}
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "aerospike.command",
						"service":  "aerospike",
						"resource": "Put",
						"type":     "aerospike",
					},
					Meta: map[string]string{
						"component": "aerospike/aerospike-client-go.v7",
						"span.kind": "client",
						"db.system": "aerospike",
					},
				},
				{
					Tags: map[string]any{
						"name":     "aerospike.command",
						"service":  "aerospike",
						"resource": "Get",
						"type":     "aerospike",
					},
					Meta: map[string]string{
						"component": "aerospike/aerospike-client-go.v7",
						"span.kind": "client",
						"db.system": "aerospike",
					},
				},
			},
		},
	}
}

// TestCaseScanAll verifies that a direct ScanAll call produces exactly one span
// named "ScanAll".  In an Orchestrion build, ScanAll delegates internally to
// ScanPartitions (also instrumented); the re-entrancy guard must suppress the
// inner span so only the outer "ScanAll" span is reported.
type TestCaseScanAll struct {
	client *as.Client
}

func (tc *TestCaseScanAll) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartAerospikeTestContainer(t)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = 2 * time.Minute
	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				tc.client, aerr = newClientReturn(host, port)
				return aerr
			},
			bo,
		),
	)
	t.Cleanup(func() { tc.client.Close() })
}

func (tc *TestCaseScanAll) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	_ = ctx // WithContext pattern not used here; GLS provides parenting.
	rs, err := tc.client.ScanAll(nil, "test", "testset")
	require.NoError(t, err)
	if rs != nil {
		rs.Close()
	}
}

func (tc *TestCaseScanAll) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "aerospike.command",
						"service":  "aerospike",
						"resource": "ScanAll",
						"type":     "aerospike",
					},
					Meta: map[string]string{
						"component": "aerospike/aerospike-client-go.v7",
						"span.kind": "client",
						"db.system": "aerospike",
					},
				},
			},
		},
	}
}

// TestCaseQuery verifies that a direct Query call produces exactly one span
// named "Query".  Query delegates internally to QueryPartitions; the guard must
// suppress the inner span.
type TestCaseQuery struct {
	client *as.Client
}

func (tc *TestCaseQuery) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartAerospikeTestContainer(t)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	bo := backoff.NewExponentialBackOff()
	bo.MaxElapsedTime = 2 * time.Minute
	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				tc.client, aerr = newClientReturn(host, port)
				return aerr
			},
			bo,
		),
	)
	t.Cleanup(func() { tc.client.Close() })
}

func (tc *TestCaseQuery) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	_ = ctx
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
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "aerospike.command",
						"service":  "aerospike",
						"resource": "Query",
						"type":     "aerospike",
					},
					Meta: map[string]string{
						"component": "aerospike/aerospike-client-go.v7",
						"span.kind": "client",
						"db.system": "aerospike",
					},
				},
			},
		},
	}
}
