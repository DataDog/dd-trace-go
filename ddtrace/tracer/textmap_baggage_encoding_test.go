// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strings"
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

// TestOTBaggageRoundTripsEncodedValues covers the ot-baggage-* prefix path
// end-to-end: a baggage key/value containing characters the injector
// percent-encodes (space, non-ASCII) must decode back to the original bytes
// on extraction through the same legacy prefix, the same way the W3C
// "baggage" header round-trips. Before the extractor decoded, "key with
// space"="Amélie DF 28" would come back as "key+with+space"="Am%C3%A9lie+DF+28".
func TestOTBaggageRoundTripsEncodedValues(t *testing.T) {
	t.Setenv(envPropagationStyle, "datadog")
	tr, err := newTracer()
	require.NoError(t, err)
	defer tr.Stop()

	ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
	ctx.setBaggageItem("key with space", "Amélie DF 28")

	out := TextMapCarrier{}
	require.NoError(t, tr.Inject(ctx, out))

	sctx, err := tr.Extract(out)
	require.NoError(t, err)

	got := map[string]string{}
	sctx.ForeachBaggageItem(func(k, v string) bool {
		got[k] = v
		return true
	})
	assert.Equal(t, "Amélie DF 28", got["key with space"])
}

// TestBaggageWireFormatIsPercentEncoded pins the on-the-wire encoding Go
// emits for both baggage styles to percent-encoding, matching dd-trace-py
// (the reference implementation for these safe-character lists) and the W3C
// baggage spec: a space becomes %20, a literal '+' stays a plus sign, and a
// literal '%' escapes to %25 so it cannot be mistaken for an escape
// sequence on a later hop.
func TestBaggageWireFormatIsPercentEncoded(t *testing.T) {
	tests := []struct {
		name  string
		style string
	}{
		{"legacy_ot_prefix", "datadog"},
		{"w3c_header", "baggage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envPropagationStyle, tc.style)
			tr, err := newTracer()
			require.NoError(t, err)
			defer tr.Stop()

			for _, v := range []struct {
				in, wire string
			}{
				{"a+b", "a+b"},                       // '+' is a literal baggage token char
				{"x y", "x%20y"},                     // space is %20, never '+'
				{"100%", "100%25"},                   // '%' must be escaped
				{"a%2Fb", "a%252Fb"},                 // escape-looking text must survive a hop
				{"a + b % c", "a%20+%20b%20%25%20c"}, // combination
				{"Am\u00e9lie", "Am%C3%A9lie"},       // non-ASCII still percent-encoded
			} {
				ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
				ctx.setBaggageItem("k", v.in)
				out := TextMapCarrier{}
				require.NoError(t, tr.Inject(ctx, out))

				var got string
				switch tc.style {
				case "datadog":
					got = out[DefaultBaggageHeaderPrefix+"k"]
				case "baggage":
					got = strings.TrimPrefix(out[DefaultBaggageHeader], "k=")
				}
				assert.Equal(t, v.wire, got, "value %q", v.in)
			}
		})
	}
}

// TestBaggageRoundTripsPlusAndPercent covers the issue #5181 corruption end
// to end: literal '+' and escape-looking '%' values in keys and values must
// survive an inject/extract hop through both the legacy ot-baggage-* prefix
// and the W3C "baggage" header.
func TestBaggageRoundTripsPlusAndPercent(t *testing.T) {
	tests := []struct {
		name  string
		style string
	}{
		{"legacy_ot_prefix", "datadog"},
		{"w3c_header", "baggage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envPropagationStyle, tc.style)
			tr, err := newTracer()
			require.NoError(t, err)
			defer tr.Stop()

			ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
			ctx.setBaggageItem("key+plus", "val+plus")
			ctx.setBaggageItem("key%2fpercent", "val%2Fpercent") // legacy lowercases keys on extract
			ctx.setBaggageItem("key with + and space", "val with + and space")
			ctx.setBaggageItem("100%", "100%")

			out := TextMapCarrier{}
			require.NoError(t, tr.Inject(ctx, out))

			sctx, err := tr.Extract(out)
			require.NoError(t, err)

			got := map[string]string{}
			sctx.ForeachBaggageItem(func(k, v string) bool {
				got[k] = v
				return true
			})
			assert.Equal(t, "val+plus", got["key+plus"])
			assert.Equal(t, "val%2Fpercent", got["key%2fpercent"])
			assert.Equal(t, "val with + and space", got["key with + and space"])
			assert.Equal(t, "100%", got["100%"])
		})
	}
}

// TestExtractForeignBaggageHeaders covers extraction of baggage injected by
// other tracers, which is where query-form unescaping corrupted values:
// dd-trace-java and dd-trace-js write legacy ot-baggage-* values verbatim (a
// literal '+' must not become a space), and dd-trace-py/dd-trace-rb
// percent-encode spaces as %20.
func TestExtractForeignBaggageHeaders(t *testing.T) {
	t.Run("legacy_ot_prefix", func(t *testing.T) {
		t.Setenv(envPropagationStyleExtract, "datadog")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		for _, tc := range []struct{ header, value, want string }{
			{"ot-baggage-java-verbatim", "a+b", "a+b"},  // java/js: verbatim
			{"ot-baggage-java-percent", "100%", "100%"}, // java/js: invalid escape kept raw
			{"ot-baggage-python-space", "x%20y", "x y"}, // python/ruby: %20
			{"ot-baggage-python-plus", "a+b", "a+b"},    // python: '+' is literal
		} {
			t.Run(tc.header, func(t *testing.T) {
				in := TextMapCarrier{
					DefaultTraceIDHeader:  "4",
					DefaultParentIDHeader: "1",
					tc.header:             tc.value,
				}
				sctx, err := tr.Extract(in)
				require.NoError(t, err)
				var got string
				sctx.ForeachBaggageItem(func(k, v string) bool {
					got = v
					return false
				})
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("w3c_header", func(t *testing.T) {
		t.Setenv(envPropagationStyleExtract, "baggage")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		for _, tc := range []struct{ header, want string }{
			{"k=a+b", "a+b"},   // literal '+' per the W3C baggage spec
			{"k=x%20y", "x y"}, // percent-encoded space
			{"k=100%", "100%"}, // invalid escape kept raw
		} {
			t.Run(tc.header, func(t *testing.T) {
				in := TextMapCarrier{DefaultBaggageHeader: tc.header}
				sctx, err := tr.Extract(in)
				require.NoError(t, err)
				var got string
				sctx.ForeachBaggageItem(func(k, v string) bool {
					got = v
					return false
				})
				assert.Equal(t, tc.want, got)
			})
		}
	})
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
