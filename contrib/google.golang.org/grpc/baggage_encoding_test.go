// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// hasControlByte reports whether s contains a raw CR, LF, or NUL byte -- the
// bytes an attacker needs to smuggle extra header lines into a carrier that
// writes header values without validation.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n', 0x00:
			return true
		}
	}
	return false
}

// otBaggageEntries returns the subset of md whose keys carry the legacy
// OpenTracing baggage prefix. gRPC metadata keys are already lowercased by
// MDCarrier.Set, matching the (also lowercase) DefaultBaggageHeaderPrefix.
func otBaggageEntries(md metadata.MD) metadata.MD {
	out := metadata.MD{}
	for k, vals := range md {
		if strings.HasPrefix(k, tracer.DefaultBaggageHeaderPrefix) {
			out[k] = vals
		}
	}
	return out
}

// TestBaggageControlCharsNotInjectedOverGRPC reproduces the outbound impact
// of a poisoned baggage value over gRPC: "v\r\nX-Evil:1" is exactly what an
// upstream "baggage: k=v%0D%0AX-Evil:1" header decodes to. gRPC metadata
// values may legally contain arbitrary bytes, so the secure behavior here is
// that the tracer itself never re-emits a raw control byte under the legacy
// ot-baggage-* prefix, regardless of what the transport would otherwise allow.
func TestBaggageControlCharsNotInjectedOverGRPC(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")

	mt := mocktracer.Start()
	defer mt.Stop()

	rig, err := newRig(true)
	require.NoError(t, err)
	defer func() { assert.NoError(t, rig.Close()) }()

	span, ctx := tracer.StartSpanFromContext(context.Background(), "x")
	span.SetBaggageItem("k", "v\r\nX-Evil:1")
	_, err = rig.client.Ping(ctx, &fixturepb.FixtureRequest{Name: "pass"})
	span.Finish()
	require.NoError(t, err, "the RPC must not fail because of a poisoned ot-baggage-* metadata value")

	md := rig.fixtureServer.LastRequestMetadata.Load().(metadata.MD)
	baggage := otBaggageEntries(md)
	for k, vals := range baggage {
		for _, v := range vals {
			assert.False(t, hasControlByte(v), "%s must not carry a raw control byte, got %q", k, v)
		}
	}
	assert.NotEmpty(t, baggage, "expected an ot-baggage-* metadata entry on the outbound request")
}
