// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"

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

	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				tc.client, aerr = newClientReturn(host, port)
				return aerr
			},
			backoff.NewExponentialBackOff(),
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

// TestCaseConcurrent verifies that concurrent calls on a shared *as.Client do
// not race or deadlock and that a span is recorded for each call. Because the
// function-body aspect relies on tracer GLS (goroutine-local), spans started
// inside the goroutines are roots rather than children of test.root.
type TestCaseConcurrent struct {
	client *as.Client
}

func (tc *TestCaseConcurrent) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartAerospikeTestContainer(t)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	require.NoError(t,
		backoff.Retry(
			func() error {
				var aerr as.Error
				tc.client, aerr = newClientReturn(host, port)
				return aerr
			},
			backoff.NewExponentialBackOff(),
		),
	)
	t.Cleanup(func() { tc.client.Close() })
}

func (tc *TestCaseConcurrent) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	const n = 3
	keys := make([]*as.Key, n)
	for i := range n {
		var err error
		keys[i], err = as.NewKey("test", "testset", fmt.Sprintf("concurrent-key-%d", i))
		require.NoError(t, err)
	}

	errs := make([]as.Error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(ctx context.Context, i int) {
			defer wg.Done()
			errs[i] = tc.client.Put(nil, keys[i], as.BinMap{"value": i})
		}(ctx, i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
}

func (tc *TestCaseConcurrent) ExpectedTraces() trace.Traces {
	putSpan := func() *trace.Trace {
		return &trace.Trace{
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
		}
	}
	// Spans started inside goroutines are roots: the function-body aspect uses
	// tracer GLS which is goroutine-local and is not copied across goroutine
	// boundaries.
	return trace.Traces{
		{Tags: map[string]any{"name": "test.root"}},
		putSpan(), putSpan(), putSpan(),
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
