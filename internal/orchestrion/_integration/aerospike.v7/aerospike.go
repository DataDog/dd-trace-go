// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	container testcontainers.Container
	client    *as.Client
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	req := testcontainers.ContainerRequest{
		Image:        "aerospike:ce-7.2.0.6",
		ExposedPorts: []string{"3000/tcp"},
		WaitingFor:   wait.ForListeningPort("3000/tcp"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
		Logger:           tclog.TestLogger(t),
	})
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, container)
	tc.container = container

	host, err := container.Host(ctx)
	require.NoError(t, err)

	mappedPort, err := container.MappedPort(ctx, "3000/tcp")
	require.NoError(t, err)
	port, err := strconv.Atoi(mappedPort.Port())
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
	span, _ := tracer.StartSpanFromContext(ctx, "test.root")
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
