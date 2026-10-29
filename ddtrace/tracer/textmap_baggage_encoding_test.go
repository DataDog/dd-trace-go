// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strings"
	"testing"

	otelbaggage "go.opentelemetry.io/otel/baggage"

	"github.com/DataDog/dd-trace-go/v2/internal/log"

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

// TestBaggageWireFormatIsPercentEncoded pins the on-the-wire encoding for
// both baggage styles. A space becomes %20. A literal '+' stays a plus
// sign. A literal '%' escapes to %25, so a later hop cannot mistake it for
// an escape sequence. go.opentelemetry.io/otel/baggage and dd-trace-rb
// emit the same shapes for W3C values. dd-trace-js escapes a literal '+'
// as %2B. dd-trace-py keeps '%' verbatim in values, so its extractor
// mis-decodes a value such as "a%2Fb".
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
				{"100%", "100%25"},                   // '%' escapes to %25
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

// TestBaggageKeyEncodingIsStyleSplit pins the key handling split between
// the two baggage styles. W3C "baggage" keys are RFC 7230 tokens. '%' and
// '+' are valid key characters, so they travel verbatim and never decode
// on extract. go.opentelemetry.io/otel/baggage reads keys the same way
// ("W3C Baggage specification does not allow percent-encoded keys").
// Legacy ot-baggage-<key> keys have no spec. Go percent-encodes both sides
// itself, so '%' escapes to %25 there and decodes back on extract.
func TestBaggageKeyEncodingIsStyleSplit(t *testing.T) {
	t.Run("w3c_key_stays_verbatim", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "baggage")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		// Keys with '%', '+' or escape-looking text must all keep their
		// identity.
		for _, key := range []string{"key%percent", "key+plus", "key%2Flooking"} {
			ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
			ctx.setBaggageItem(key, "v")
			out := TextMapCarrier{}
			require.NoError(t, tr.Inject(ctx, out))
			assert.Equal(t, key+"=v", out[DefaultBaggageHeader], "key %q", key)

			sctx, err := tr.Extract(out)
			require.NoError(t, err)
			got := map[string]string{}
			sctx.ForeachBaggageItem(func(k, v string) bool {
				got[k] = v
				return true
			})
			assert.Equal(t, "v", got[key], "key %q must round-trip verbatim", key)
		}
	})

	t.Run("w3c_foreign_key_not_decoded", func(t *testing.T) {
		// An OTel/JS app injects keys as opaque tokens. Extraction must not
		// change a key's identity, even when the key contains what looks
		// like an escape sequence.
		t.Setenv(envPropagationStyleExtract, "baggage")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		in := TextMapCarrier{DefaultBaggageHeader: "k%2Fx=v+b"}
		sctx, err := tr.Extract(in)
		require.NoError(t, err)
		got := map[string]string{}
		sctx.ForeachBaggageItem(func(k, v string) bool {
			got[k] = v
			return true
		})
		assert.Equal(t, map[string]string{"k%2Fx": "v+b"}, got)
	})

	t.Run("legacy_key_escapes_percent", func(t *testing.T) {
		t.Setenv(envPropagationStyle, "datadog")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
		ctx.setBaggageItem("key%percent", "v")
		out := TextMapCarrier{}
		require.NoError(t, tr.Inject(ctx, out))
		assert.Equal(t, "v", out[DefaultBaggageHeaderPrefix+"key%25percent"])

		sctx, err := tr.Extract(out)
		require.NoError(t, err)
		got := map[string]string{}
		sctx.ForeachBaggageItem(func(k, v string) bool {
			got[k] = v
			return true
		})
		// The legacy extractor lowercases keys before decoding.
		assert.Equal(t, "v", got["key%percent"])
	})
}

// TestW3CNonTokenKeyDroppedOnInject pins the handling of a key outside
// the RFC 7230 token set. Such a key cannot appear on the wire without
// changing its identity. Go drops the member on inject and logs a
// warning. OTel and dd-trace-js reject these members too. Values with
// non-token characters stay injected, because the W3C grammar
// percent-encodes values.
func TestW3CNonTokenKeyDroppedOnInject(t *testing.T) {
	tp := new(log.RecordLogger)
	tp.Ignore(commonLogIgnore...)
	t.Setenv(envPropagationStyle, "baggage")
	tr, err := newTracer(WithLogger(tp))
	require.NoError(t, err)
	defer tr.Stop()
	tp.Reset()

	ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
	ctx.setBaggageItem("key with space", "v")
	ctx.setBaggageItem("okkey", "value with space")

	out := TextMapCarrier{}
	require.NoError(t, tr.Inject(ctx, out))
	assert.Equal(t, "okkey=value%20with%20space", out[DefaultBaggageHeader])

	sctx, err := tr.Extract(out)
	require.NoError(t, err)
	got := map[string]string{}
	sctx.ForeachBaggageItem(func(k, v string) bool {
		got[k] = v
		return true
	})
	assert.Equal(t, map[string]string{"okkey": "value with space"}, got)

	logs := tp.Logs()
	require.NotEmpty(t, logs, "the drop must log a warning")
	assert.Contains(t, logs[0], `baggage key "key with space" is outside the W3C token set, dropping item`)
}

// TestDecodeBaggageFieldMixedEscapes pins the invalid-escape policy of
// decodeBaggageField. One invalid sequence keeps the WHOLE field raw.
// dd-trace-py decodes the valid sequences and keeps the invalid ones, and
// go.opentelemetry.io/otel/baggage rejects the member entirely. Go
// injects always escape '%', so only foreign or hand-written headers
// reach this path.
func TestDecodeBaggageFieldMixedEscapes(t *testing.T) {
	assert.Equal(t, "a%2Bb%C", decodeBaggageField("a%2Bb%C"), "one bad escape keeps the whole field raw")
	assert.Equal(t, "a+b", decodeBaggageField("a%2Bb"), "all-valid escapes still decode")
	assert.Equal(t, "100%", decodeBaggageField("100%"), "a lone '%' is not a discard trigger")
}

// TestBaggageRoundTripsPlusAndPercent covers the issue #5181 corruption
// end to end. Literal '+' and escape-looking '%' values must survive an
// inject/extract hop. The test runs the hop through both the legacy
// ot-baggage-* prefix and the W3C "baggage" header.
func TestBaggageRoundTripsPlusAndPercent(t *testing.T) {
	tests := []struct {
		name  string
		style string
		// Extra key/value pair specific to the style. The W3C key grammar is
		// the RFC 7230 token set. A key that contains a space is out-of-spec
		// there, and OTel/JS reject such members outright. The legacy prefix
		// has no key grammar, so its round trip still covers space-bearing
		// keys.
		extraKey, extraVal string
	}{
		{"legacy_ot_prefix", "datadog", "key with + and space", "val with + and space"},
		{"w3c_header", "baggage", "token-key", "val with + and space"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envPropagationStyle, tc.style)
			tr, err := newTracer()
			require.NoError(t, err)
			defer tr.Stop()

			ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
			// Keys valid in both styles. '%', '+' and space are legacy-safe,
			// and '%' and '+' are W3C token characters.
			ctx.setBaggageItem("key+plus", "val+plus")
			ctx.setBaggageItem("key%2fpercent", "val%2Fpercent")
			ctx.setBaggageItem("100%", "100%")
			ctx.setBaggageItem(tc.extraKey, tc.extraVal)

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
			assert.Equal(t, "100%", got["100%"])
			assert.Equal(t, tc.extraVal, got[tc.extraKey])
		})
	}
}

// TestGoInjectedBaggageParsesUnderOTel proves W3C wire-format parity with
// the real reference parser. A header injected by this tracer's "baggage"
// style must parse under go.opentelemetry.io/otel/baggage with the
// identical key and the original value bytes. This covers literal '+' and
// '%' and escape-looking text.
func TestGoInjectedBaggageParsesUnderOTel(t *testing.T) {
	t.Setenv(envPropagationStyle, "baggage")
	tr, err := newTracer()
	require.NoError(t, err)
	defer tr.Stop()

	for _, tc := range []struct{ key, val string }{
		{"key+plus", "val+plus"},
		{"key%percent", "100%"},
		{"key%2Flooking", "a%2Fb"}, // escape-looking text must not decode further
		{"space", "val with + and space"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			ctx := &SpanContext{traceID: traceIDFrom64Bits(1), spanID: 1}
			ctx.setBaggageItem(tc.key, tc.val)
			out := TextMapCarrier{}
			require.NoError(t, tr.Inject(ctx, out))

			header := out[DefaultBaggageHeader]
			parsed, err := otelbaggage.Parse(header)
			require.NoError(t, err, "header %q must parse under the OTel parser", header)

			member := parsed.Member(tc.key)
			require.Equal(t, tc.key, member.Key(), "key must survive as an opaque token")
			assert.Equal(t, tc.val, member.Value(), "value must decode to the original bytes")
		})
	}
}

// TestExtractForeignBaggageHeaders covers extraction of baggage injected
// by other tracers. On the legacy ot-baggage-* path, dd-trace-js and
// dd-trace-py inject values verbatim, so a literal '+' must stay a plus
// sign on extract. dd-trace-java percent-encodes legacy values on inject
// (URLEncoder, so a literal '+' goes out as %2B), but its extractor
// query-decodes them, and java.net.URLDecoder turns '+' into a space. On
// the W3C "baggage" header, dd-trace-py and dd-trace-rb percent-encode
// spaces as %20 and leave '+' literal. dd-trace-js emits %2B for a
// literal '+' (encodeURIComponent).
func TestExtractForeignBaggageHeaders(t *testing.T) {
	t.Run("legacy_ot_prefix", func(t *testing.T) {
		t.Setenv(envPropagationStyleExtract, "datadog")
		tr, err := newTracer()
		require.NoError(t, err)
		defer tr.Stop()

		for _, tc := range []struct{ header, value, want string }{
			{"ot-baggage-js-verbatim-plus", "a+b", "a+b"},      // js/py: verbatim
			{"ot-baggage-js-verbatim-percent", "100%", "100%"}, // js/py: invalid escape kept raw
			{"ot-baggage-encoded-space", "x%20y", "x y"},       // go/java: %20 (the only legacy injectors that percent-encode)
			{"ot-baggage-java-plus", "a%2Bb", "a+b"},           // java: %2B (URLEncoder)
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
			{"k=a+b", "a+b"},   // literal '+' per the W3C baggage spec (py/ruby/otel inject it verbatim)
			{"k=a%2Bb", "a+b"}, // js: encodeURIComponent escapes a literal '+' as %2B
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
