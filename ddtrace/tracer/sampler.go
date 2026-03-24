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
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/locking/assert"
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
	rate float64 // +checklocks:RWMutex
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
// +checklocksignore — Fast path reads r.rate without lock (deliberate); s.traceID is immutable after init.
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

// rampUpInterval is the minimum duration between successive 2x rate increases.
const rampUpInterval = time.Second

// defaultSampler is the fallback sampling strategy used when no user-defined
// trace sampling rules match. In "Datadog" mode this is the prioritySampler;
// in OTLP export mode this is the otelParentBasedAlwaysOnSampler.
type defaultSampler interface {
	apply(s *Span)
}

// otelParentBasedAlwaysOnSampler implements parentbased_always_on: it honors
// propagated sampling decisions from parents, else keeps every span at rate 1.0.
type otelParentBasedAlwaysOnSampler struct{}

func newOtelParentBasedAlwaysOnSampler() *otelParentBasedAlwaysOnSampler {
	return &otelParentBasedAlwaysOnSampler{}
}

// +checklocksignore — Called during initialization in StartSpan, span not yet shared.
func (s *otelParentBasedAlwaysOnSampler) apply(spn *Span) {
	spn.setSamplingPriority(ext.PriorityAutoKeep, samplernames.Default)
	spn.SetTag(keySamplingPriorityRate, 1.0)
}

// prioritySampler holds a set of per-service sampling rates and applies
// them to spans.
type prioritySampler struct {
	mu               locking.RWMutex
	rates            map[serviceEnvKey]float64 // +checklocks:mu
	defaultRate      float64                   // +checklocks:mu
	agentRatesLoaded bool                      // +checklocks:mu
	lastCapped       time.Time                 // +checklocks:mu
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
		if before, after0, ok0 := strings.Cut(after, ",env:"); ok0 {
			k.service = before
			k.env = after0
		}
	}
	return k
}

// cappedRate returns a rate that is at most 2x the old rate when increasing.
// Rate decreases and transitions from zero are applied immediately.
// When canIncrease is false (cooldown not elapsed), increases are held at oldRate.
func cappedRate(oldRate, newRate float64, canIncrease bool) (float64, bool) {
	if newRate <= oldRate || oldRate == 0 {
		return newRate, false
	}
	if !canIncrease {
		return oldRate, false
	}
	return min(oldRate*2, newRate), true
}

// readRatesJSON will try to read the rates as JSON from the given io.ReadCloser.
// When a new rate for a service is higher than the current rate, the increase is
// capped at 2x the current rate (at most once per rampUpInterval). This prevents
// a spike in sampled traces when the agent restarts and temporarily reports
// rate=1.0 for all services.
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
	ps.agentRatesLoaded = true
	now := time.Now()
	canIncrease := ps.lastCapped.IsZero() || now.Sub(ps.lastCapped) >= rampUpInterval
	capApplied := false
	for key, newRate := range rates {
		oldRate, ok := ps.rates[key]
		if !ok {
			oldRate = ps.defaultRate
		}
		rate, applied := cappedRate(oldRate, newRate, canIncrease)
		capApplied = capApplied || applied
		rates[key] = rate
	}
	if canIncrease && capApplied {
		ps.lastCapped = now
	}
	ps.rates = rates
	if v, ok := ps.rates[defaultRateKey]; ok {
		ps.defaultRate = v
		delete(ps.rates, defaultRateKey)
	}
	return nil
}

// getRate returns the sampling rate to be used for the given span. Callers must
// guard the span.
// +checklocksignore — Called during initialization in StartSpan, span not yet shared.
func (ps *prioritySampler) getRate(spn *Span) float64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.getRateLocked(spn)
}

// getRateLocked returns the sampling rate for the given span.
// Caller must hold ps.mu (at least RLock).
// +checklocksignore — Called during initialization in StartSpan, span not yet shared.
func (ps *prioritySampler) getRateLocked(spn *Span) float64 {
	assert.RWMutexRLocked(&ps.mu)
	key := serviceEnvKey{service: spn.service, env: spn.meta[ext.Environment]}
	if rate, ok := ps.rates[key]; ok {
		return rate
	}
	return ps.defaultRate
}

// getDefaultRate returns the default sampling rate.
func (ps *prioritySampler) getDefaultRate() float64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.defaultRate
}

// apply applies sampling priority to the given span. Caller must ensure it is safe
// to modify the span.
// +checklocksignore — Called during initialization in StartSpan, span not yet shared.
func (ps *prioritySampler) apply(spn *Span) {
	ps.mu.RLock()
	rate := ps.getRateLocked(spn)
	fromAgent := ps.agentRatesLoaded
	ps.mu.RUnlock()
	if sampledByRate(spn.traceID, rate) {
		spn.setSamplingPriority(ext.PriorityAutoKeep, samplernames.AgentRate)
	} else {
		spn.setSamplingPriority(ext.PriorityAutoReject, samplernames.AgentRate)
	}
	spn.SetTag(keySamplingPriorityRate, rate)
	// Only set the Knuth sampling rate tag when actual agent rates have been
	// received. The initial default rate (1.0) is a client-side fallback that
	// does not represent an agent-configured rate, so it must not propagate
	// as _dd.p.ksr to stay consistent with other tracers.
	if fromAgent {
		spn.SetTag(keyKnuthSamplingRate, formatKnuthSamplingRate(rate))
	}
}
