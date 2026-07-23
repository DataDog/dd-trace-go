// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// TestOTBaggageEnforcesLimitsOverGRPC covers the ot-baggage-* prefix path
// over the gRPC client interceptor: unlike the W3C "baggage" header, this
// path currently has no item-count or byte-size cap, so an attacker-sized
// baggage set is fully delivered as outbound gRPC metadata.
func TestOTBaggageEnforcesLimitsOverGRPC(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")

	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true)
	require.NoError(t, err)
	defer func() { assert.NoError(t, rig.Close()) }()

	span, ctx := tracer.StartSpanFromContext(context.Background(), "x")
	for i := range 100 {
		span.SetBaggageItem(fmt.Sprintf("k%d", i), "x")
	}
	_, err = rig.client.Ping(ctx, &fixturepb.FixtureRequest{Name: "pass"})
	span.Finish()
	require.NoError(t, err)

	md := rig.fixtureServer.LastRequestMetadata.Load().(metadata.MD)
	count, totalBytes := 0, 0
	for k, vals := range otBaggageEntries(md) {
		for _, v := range vals {
			count++
			totalBytes += len(k) + len(v)
		}
	}
	assert.LessOrEqual(t, count, 64)
	assert.LessOrEqual(t, totalBytes, 8192)
}
