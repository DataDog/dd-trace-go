// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestBaggageControlCharsNotInjectedRaw covers the ot-baggage-* injection
// path: a baggage value carrying percent-encoded control bytes is decoded on
// extraction, and must not reach an outbound carrier as a raw CR/LF/NUL once
// re-injected under the legacy OpenTracing baggage prefix.
func TestBaggageControlCharsNotInjectedRaw(t *testing.T) {
	// Extraction uses the full default style set (matching production
	// defaults); injection is narrowed to "datadog" -- the propagator that
	// actually re-emits baggage without encoding -- so the test targets that
	// component instead of tripping over unrelated injector behavior.
	t.Setenv(envPropagationStyleExtract, "datadog,tracecontext,baggage")
	t.Setenv(envPropagationStyleInject, "datadog")

	tests := []struct {
		name    string
		encoded string
	}{
		{"crlf", "v%0D%0AX-Evil:1"},
		{"cr_only", "v%0D"},
		{"lf_only", "v%0A"},
		{"nul_byte", "v%00evil"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := newTracer()
			require.NoError(t, err)
			defer tr.Stop()

			in := TextMapCarrier{
				DefaultTraceIDHeader:  "4",
				DefaultParentIDHeader: "1",
				DefaultBaggageHeader:  "k=" + tc.encoded,
			}
			sctx, err := tr.Extract(in)
			require.NoError(t, err)

			out := TextMapCarrier{}
			require.NoError(t, tr.Inject(sctx, out))

			injected := DefaultBaggageHeaderPrefix + "k"
			assert.False(t, hasControlByte(out[injected]),
				"%s must not carry a raw control byte, got %q", injected, out[injected])
		})
	}
}

// TestBaggageControlCharsNotInjectedRawFromDirectContext isolates the
// injector from extraction: baggage set directly on a span context (as
// SetBaggageItem would) must still be sanitized when re-injected under the
// legacy ot-baggage-* prefix.
func TestBaggageControlCharsNotInjectedRawFromDirectContext(t *testing.T) {
	t.Setenv(envPropagationStyle, "datadog")
	tr, err := newTracer()
	require.NoError(t, err)
	defer tr.Stop()

	ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
	ctx.setBaggageItem("k", "v\r\nX-Evil:1")

	out := TextMapCarrier{}
	require.NoError(t, tr.Inject(ctx, out))

	injected := DefaultBaggageHeaderPrefix + "k"
	assert.False(t, hasControlByte(out[injected]),
		"%s must not carry a raw control byte, got %q", injected, out[injected])
}
