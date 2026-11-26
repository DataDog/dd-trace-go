// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"testing"

	as "github.com/aerospike/aerospike-client-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// store gets the Client methods through an embedded pointer.
type store struct {
	*as.Client
}

// valueStore gets them through an embedded as.Client value, inside a field
// of an unexported type.
type valueStore struct {
	inner
}

type inner struct {
	as.Client
}

// TestCaseEmbedded calls Client methods promoted through an embedded field.
type TestCaseEmbedded struct {
	client *as.Client
}

func (tc *TestCaseEmbedded) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion does not trace a Client method promoted through an embedded field")
	}
	tc.client = newClient(t)
}

func (tc *TestCaseEmbedded) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	key, err := as.NewKey("test", "testset", "embedded-key")
	require.NoError(t, err)

	s := &store{Client: tc.client}
	require.NoError(t, s.Put(nil, key, as.BinMap{"value": "embedded"}))

	vs := valueStore{inner{Client: *tc.client}}
	_, err = vs.Get(nil, key)
	require.NoError(t, err)
}

func (*TestCaseEmbedded) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: trace.Traces{aerospikeChild("Put"), aerospikeChild("Get")},
		},
	}
}
