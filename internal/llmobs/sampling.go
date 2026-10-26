// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"bytes"
	"math"
	"strconv"
)

const (
	// SamplingDecisionKeep marks a trace whose content is kept.
	SamplingDecisionKeep = "1"
	// SamplingDecisionDrop marks a trace whose content is dropped by the backend.
	// The span events are still submitted so token and cost metrics stay complete.
	SamplingDecisionDrop = "0"
)

// knuthFactor matches the APM rate sampler and the other tracers, so every SDK
// in a distributed trace reaches the same decision for the same trace ID.
const knuthFactor = uint64(1111111111111111111)

// samplingDecision is a trace's LLMObs sampling decision. It is decided once on
// the LLMObs root span and inherited by every descendant, in-process and across
// services. A zero value means undecided: the span continues an upstream trace
// whose SDK did not propagate a decision.
type samplingDecision struct {
	rate     string
	decision string
}

// rootSamplingDecision decides whether the LLMObs trace rooted at a span with the
// given APM trace ID (hex encoded) is kept at the given rate.
func rootSamplingDecision(apmTraceID string, rate float64) samplingDecision {
	d := samplingDecision{rate: formatSampleRate(rate), decision: SamplingDecisionDrop}
	if sampledByRate(lower64TraceID(apmTraceID), rate) {
		d.decision = SamplingDecisionKeep
	}
	return d
}

func sampledByRate(traceID uint64, rate float64) bool {
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	return traceID*knuthFactor <= uint64(rate*math.MaxUint64)
}

// lower64TraceID returns the low 64 bits of a hex encoded trace ID, which is what
// the APM rate sampler hashes.
func lower64TraceID(hexTraceID string) uint64 {
	if len(hexTraceID) > 16 {
		hexTraceID = hexTraceID[len(hexTraceID)-16:]
	}
	v, err := strconv.ParseUint(hexTraceID, 16, 64)
	if err != nil {
		return 0
	}
	return v
}

// formatSampleRate formats a rate with up to 6 decimal digits and no trailing
// zeros ("1", "0.5"), the same wire format as the other tracers.
func formatSampleRate(rate float64) string {
	var buf [24]byte
	b := strconv.AppendFloat(buf[:0], rate, 'f', 6, 64)
	if i := bytes.IndexByte(b, '.'); i >= 0 {
		end := len(b)
		for end > i+1 && b[end-1] == '0' {
			end--
		}
		if b[end-1] == '.' {
			end--
		}
		b = b[:end]
	}
	return string(b)
}
