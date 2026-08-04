// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package redigomanual

import (
	"context"
	"testing"

	redigotrace "github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/cenkalti/backoff/v4"
	"github.com/gomodule/redigo/redis"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// wovenBuild reports whether this build was auto-instrumented by either tool.
var wovenBuild = built.WithOrchestrion || otelc.Enabled()

// TestManualDialIsNotDoubleWrapped pins the orchestrion behaviour: when an
// application dials through the contrib itself in an auto-instrumented build, the
// connection is wrapped once, so one command produces one span.
//
// Orchestrion gets this right by construction. It rewrites redis.Dial* call sites
// in application code and never rewrites the contrib's own redis.DialContext
// call, so the two cannot both apply. A hook on the redis.DialContext definition
// has no such boundary and can wrap a connection the contrib already wrapped.
func TestManualDialIsNotDoubleWrapped(t *testing.T) {
	if !wovenBuild {
		t.Skip("only meaningful in an auto-instrumented build")
	}
	containers.SkipIfProviderIsNotHealthy(t)

	_, addr := containers.StartRedisTestContainer(t)

	// Dialed before the tracer starts, and retried: a freshly started container
	// refuses the first connections, and the main redigo suite retries for the
	// same reason. Dialing first also keeps the retries out of the span count,
	// which is what this test measures.
	conn, err := backoff.RetryWithData(
		func() (redis.Conn, error) {
			return redigotrace.DialContext(context.Background(), "tcp", addr)
		},
		backoff.NewExponentialBackOff(),
	)
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close()) }()

	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)

	_, err = conn.Do("SET", uuid.NewString(), "value")
	require.NoError(t, err)

	// Stop rather than Flush, matching the harness: it is what guarantees the
	// span has reached the agent before the count is read.
	tr.Stop()

	assert.Equalf(t, 1, agent.CountSpans(),
		"one command through a manually dialed connection must produce exactly one span; "+
			"more than one means the connection was wrapped twice, once by the contrib and "+
			"once by the auto-instrumentation")
}
