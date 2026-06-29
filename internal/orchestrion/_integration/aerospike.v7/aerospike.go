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

// TestCaseConcurrent verifies that spans started in goroutines are correctly
// linked to a parent span when Orchestrion's injected WithContext method is
// used to propagate context across goroutine boundaries.  Without the
// struct-definition aspect that injects WithContext into *as.Client, the
// interface assertion below returns the client unchanged, GLS cannot cross the
// goroutine boundary, and the Put spans would be roots — causing the test to
// fail.
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

	// withContext resolves the WithContext method that Orchestrion's
	// struct-definition aspect injects into *as.Client at compile time.  The
	// interface assertion compiles and runs without Orchestrion (returning c
	// unchanged, so GLS would be the fallback — which cannot cross goroutine
	// boundaries and would make the spans roots); with Orchestrion it stores ctx
	// in a goroutine-keyed map consumed by the __ddGetCtx() call in the
	// method-call advice template, producing correct parent–child spans.
	withContext := func(c *as.Client, ctx context.Context) *as.Client {
		type withContexter interface {
			WithContext(context.Context) *as.Client
		}
		if wc, ok := any(c).(withContexter); ok {
			return wc.WithContext(ctx)
		}
		return c
	}

	errs := make([]as.Error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			// ctx is captured from the outer scope, not a function argument of
			// this goroutine. Orchestrion's method-call template cannot find a
			// context.Context parameter here (else branch → __ddGetCtx()), so
			// the context must be stored first via the injected WithContext.
			errs[i] = withContext(tc.client, ctx).Put(nil, keys[i], as.BinMap{"value": i})
		}(i)
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
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				putSpan(), putSpan(), putSpan(),
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
