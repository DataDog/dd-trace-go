// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"strconv"
	"strings"
)

// OpenTelemetry consistent probability sampling (OTEP 235) carries two 56-bit
// values in the `ot=` tracestate list-member:
//   - rv: explicit randomness, 14 hex digits.
//   - th: rejection threshold, hex with trailing zero nibbles trimmed.
//
// A hop keeps the trace when rv >= th. DD's existing Knuth-hash decision
// (h = traceIDLower * knuthFactor mod 2^64; keep if h <= rate*MaxUint64) is
// losslessly re-expressed as an (rv, th) pair, so a DD probability decision can
// be emitted in an OTel-compliant form without changing the decision.
// otelRVHexLen is the fixed hex width of an rv value (56 bits).
const otelRVHexLen = 14

// deriveOtelRV re-expresses DD's Knuth hash of the trace ID as an OTel
// randomness value: flip the comparison direction (^h) and drop the low 8 bits
// to keep the top 56, which preserves the ordering the keep/drop comparison
// relies on.
func deriveOtelRV(traceIDLower uint64) uint64 {
	h := traceIDLower * knuthFactor // wraps mod 2^64, same hash as sampledByRate
	return (^h) >> 8
}

// deriveOtelTH expresses a sampling rate as a 56-bit rejection threshold,
// th = round((1 - rate) * 2^56) with round-to-nearest, ties away from zero.
// The caller must only pass a rate in (0, 1]; a rate of 0 has no representable
// threshold (see setOtelProbability).
func deriveOtelTH(rate float64) uint64 {
	return uint64(math.Round((1 - rate) * float64(uint64(1)<<56)))
}

// parseOtelTracestate parses the value of an `ot=` list-member (the part after
// "ot="), reading the rv and th sub-keys. A malformed or absent sub-key is
// reported as not-OK so callers can treat it as absent, per the spec-consistent
// robustness rule: never reject the trace over a bad `ot=`.
func parseOtelTracestate(value string) (rv uint64, rvOK bool, th uint64, thOK bool) {
	for member := range strings.SplitSeq(value, ";") {
		k, v, ok := strings.Cut(member, ":")
		if !ok {
			continue
		}
		switch k {
		case "rv":
			// rv is exactly 14 lowercase hex digits (56 bits).
			if len(v) == otelRVHexLen && isValidID(v) {
				if n, err := strconv.ParseUint(v, 16, 64); err == nil {
					rv, rvOK = n, true
				}
			}
		case "th":
			// th has trailing zero nibbles trimmed; right-pad back to 14 digits
			// to recover the full 56-bit value.
			if len(v) >= 1 && len(v) <= otelRVHexLen && isValidID(v) {
				padded := v + strings.Repeat("0", otelRVHexLen-len(v))
				if n, err := strconv.ParseUint(padded, 16, 64); err == nil {
					th, thOK = n, true
				}
			}
		}
	}
	return rv, rvOK, th, thOK
}

// appendOtelValue writes the value of the `ot=` list-member (without the "ot="
// prefix) to b. It emits rv as 14 hex digits and, when thSet, th with trailing
// zero nibbles trimmed. Callers must only invoke this when rv is present, since
// emitting th without rv is forbidden by the pairing invariant.
func appendOtelValue(b *strings.Builder, rv uint64, th uint64, thSet bool) {
	b.WriteString("rv:")
	var buf [otelRVHexLen]byte
	hexEncode14(&buf, rv)
	b.Write(buf[:])
	if !thSet {
		return
	}
	b.WriteString(";th:")
	hexEncode14(&buf, th)
	// Trim trailing zero nibbles; a th of 0 is written as a single "0".
	end := otelRVHexLen
	for end > 1 && buf[end-1] == '0' {
		end--
	}
	b.Write(buf[:end])
}

// hexEncode14 writes n as exactly 14 lowercase hex digits (56 bits, big-endian)
// into buf. Only the low 56 bits of n are represented.
func hexEncode14(buf *[otelRVHexLen]byte, n uint64) {
	const digits = "0123456789abcdef"
	for i := otelRVHexLen - 1; i >= 0; i-- {
		buf[i] = digits[n&0xf]
		n >>= 4
	}
}
