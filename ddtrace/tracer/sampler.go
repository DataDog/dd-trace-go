// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
)

// Sampler is an interface for sampling traces.
type Sampler interface {
	// Sample returns true if the given span should be sampled.
	Sample(span *Span) bool
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

type customSampler struct {
	s Sampler
}

// Rate implements RateSampler.
func (*customSampler) Rate() float64 {
	return 1.0
}

// SetRate implements RateSampler.
func (*customSampler) SetRate(_ float64) {
	// noop
}

func (s *customSampler) Sample(span *Span) bool {
	return s.s.Sample(span)
}

// rateSampler samples from a sample rate.
type rateSampler struct {
	locking.RWMutex
	rate float64
}

// NewAllSampler is a short-hand for NewRateSampler(1). It is all-permissive.
func NewAllSampler() RateSampler { return NewRateSampler(1) }

// NewRateSampler returns an initialized RateSampler with a given sample rate.
func NewRateSampler(rate float64) RateSampler {
	if rate > 1.0 {
		rate = 1.0
	}
	if rate < 0.0 {
		rate = 0.0
	}
	return &rateSampler{rate: rate}
}

// Rate returns the current rate of the sampler.
func (r *rateSampler) Rate() float64 {
	r.RLock()
	defer r.RUnlock()
	return r.rate
}

// SetRate sets a new sampling rate.
func (r *rateSampler) SetRate(rate float64) {
	r.Lock()
	r.rate = rate
	r.Unlock()
}

// constants used for the Knuth hashing, same as agent.
const knuthFactor = uint64(1111111111111111111)

// Sample returns true if the given span should be sampled.
func (r *rateSampler) Sample(s *Span) bool {
	if r.rate == 1 {
		// fast path
		return true
	}
	if r.rate == 0 || s == nil {
		return false
	}
	r.RLock()
	defer r.RUnlock()
	return sampledByRate(s.traceID, r.rate)
}

// sampledByRate verifies if the number n should be sampled at the specified
// rate.
func sampledByRate(n uint64, rate float64) bool {
	if rate == 1 {
		return true
	}
	if rate == 0 {
		return false
	}

	return n*knuthFactor <= uint64(rate*math.MaxUint64)
}

// formatKnuthSamplingRate formats a sampling rate as a string with up to 6 decimal digits
func formatKnuthSamplingRate(rate float64) string {
	return strconv.FormatFloat(rate, 'g', 6, 64)
}

// serviceEnvKey is used as a map key for per-service sampling rates,
// avoiding string concatenation on every lookup.
type serviceEnvKey struct {
	service, env string
}

// prioritySampler holds a set of per-service sampling rates and applies
// them to spans.
type prioritySampler struct {
	mu          locking.RWMutex
	rates       map[serviceEnvKey]float64
	defaultRate float64
}

func newPrioritySampler() *prioritySampler {
	return &prioritySampler{
		rates:       make(map[serviceEnvKey]float64),
		defaultRate: 1.,
	}
}

// parseServiceEnvKey parses a "service:XXX,env:YYY" string into a serviceEnvKey.
// It splits at the first ",env:" after the prefix so that env values containing
// that token are preserved (e.g. "service:foo,env:bar,env:baz" -> service="foo", env="bar,env:baz").
// This preserves the original behavior when the key was a string concatenation of "service:" and the env value.
func parseServiceEnvKey(s string) serviceEnvKey {
	var k serviceEnvKey
	if after, ok := strings.CutPrefix(s, "service:"); ok {
		if i := strings.Index(after, ",env:"); i >= 0 {
			k.service = after[:i]
			k.env = after[i+len(",env:"):]
		}
	}
	return k
}

// readRatesJSON will try to read the rates as JSON from the given io.ReadCloser.
func (ps *prioritySampler) readRatesJSON(rc io.ReadCloser) error {
	var payload struct {
		Rates map[string]float64 `json:"rate_by_service"`
	}
	if err := json.NewDecoder(rc).Decode(&payload); err != nil {
		return err
	}
	rc.Close()
	var defaultRateKey serviceEnvKey
	rates := make(map[serviceEnvKey]float64, len(payload.Rates))
	for k, v := range payload.Rates {
		rates[parseServiceEnvKey(k)] = v
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.rates = rates
	if v, ok := ps.rates[defaultRateKey]; ok {
		ps.defaultRate = v
		delete(ps.rates, defaultRateKey)
	}
	return nil
}

// getRate returns the sampling rate to be used for the given span. Callers must
// guard the span.
func (ps *prioritySampler) getRate(spn *Span) float64 {
	key := serviceEnvKey{service: spn.service, env: spn.meta[ext.Environment]}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if rate, ok := ps.rates[key]; ok {
		return rate
	}
	return ps.defaultRate
}

// apply applies sampling priority to the given span. Caller must ensure it is safe
// to modify the span.
func (ps *prioritySampler) apply(spn *Span) {
	rate := ps.getRate(spn)
	if sampledByRate(spn.traceID, rate) {
		spn.setSamplingPriority(ext.PriorityAutoKeep, samplernames.AgentRate)
	} else {
		spn.setSamplingPriority(ext.PriorityAutoReject, samplernames.AgentRate)
	}
	spn.SetTag(keySamplingPriorityRate, rate)
	// Set the Knuth sampling rate tag when sampled by agent rate
	spn.SetTag(keyKnuthSamplingRate, formatKnuthSamplingRate(rate))
}
