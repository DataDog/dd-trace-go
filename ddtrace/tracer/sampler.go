// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
)

// Sampler is the generic interface of any sampler. It must be safe for concurrent use.
type Sampler interface {
	// Sample returns true if the given span should be sampled.
	Sample(span Span) bool
}

// RateSampler is a sampler implementation which randomly selects spans using a
// provided rate. For example, a rate of 0.75 will permit 75% of the spans.
// RateSampler implementations should be safe for concurrent use.
type RateSampler interface {
	Sampler

	// Rate returns the current sample rate.
	Rate() float64

	// SetRate sets a new sample rate.
	SetRate(rate float64)
}

type samplerV2Adapter struct {
	sampler v2.RateSampler
}

// Rate implements RateSampler.
func (sa samplerV2Adapter) Rate() float64 {
	return sa.sampler.Rate()
}

// Sample implements RateSampler.
func (sa samplerV2Adapter) Sample(span ddtrace.Span) bool {
	s, ok := span.(internal.SpanV2Adapter)
	if !ok {
		return false
	}
	return sa.sampler.Sample(s.Span)
}

// SetRate implements RateSampler.
func (sa samplerV2Adapter) SetRate(rate float64) {
	sa.sampler.SetRate(rate)
}

// NewAllSampler is a short-hand for NewRateSampler(1). It is all-permissive.
func NewAllSampler() RateSampler {
	return samplerV2Adapter{
		sampler: v2.NewRateSampler(1),
	}
}

// NewRateSampler returns an initialized RateSampler with a given sample rate.
func NewRateSampler(rate float64) RateSampler {
	return samplerV2Adapter{
		sampler: v2.NewRateSampler(rate),
	}
}
