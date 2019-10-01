// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"golang.org/x/time/rate"
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

// rateSampler samples from a sample rate.
type rateSampler struct {
	sync.RWMutex
	rate float64
}

// NewAllSampler is a short-hand for NewRateSampler(1). It is all-permissive.
func NewAllSampler() RateSampler { return NewRateSampler(1) }

// NewRateSampler returns an initialized RateSampler with a given sample rate.
func NewRateSampler(rate float64) RateSampler {
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
func (r *rateSampler) Sample(spn ddtrace.Span) bool {
	if r.rate == 1 {
		// fast path
		return true
	}
	s, ok := spn.(*span)
	if !ok {
		return false
	}
	r.RLock()
	defer r.RUnlock()
	return sampledByRate(s.TraceID, r.rate)
}

// sampledByRate verifies if the number n should be sampled at the specified
// rate.
func sampledByRate(n uint64, rate float64) bool {
	if rate < 1 {
		return n*knuthFactor < uint64(rate*math.MaxUint64)
	}
	return true
}

// prioritySampler holds a set of per-service sampling rates and applies
// them to spans.
type prioritySampler struct {
	mu          sync.RWMutex
	rates       map[string]float64
	defaultRate float64
}

func newPrioritySampler() *prioritySampler {
	return &prioritySampler{
		rates:       make(map[string]float64),
		defaultRate: 1.,
	}
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
	const defaultRateKey = "service:,env:"
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.rates = payload.Rates
	if v, ok := ps.rates[defaultRateKey]; ok {
		ps.defaultRate = v
		delete(ps.rates, defaultRateKey)
	}
	return nil
}

// getRate returns the sampling rate to be used for the given span. Callers must
// guard the span.
func (ps *prioritySampler) getRate(spn *span) float64 {
	key := "service:" + spn.Service + ",env:" + spn.Meta[ext.Environment]
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if rate, ok := ps.rates[key]; ok {
		return rate
	}
	return ps.defaultRate
}

// apply applies sampling priority to the given span. Caller must ensure it is safe
// to modify the span.
func (ps *prioritySampler) apply(spn *span) {
	rate := ps.getRate(spn)
	if sampledByRate(spn.TraceID, rate) {
		spn.SetTag(ext.SamplingPriority, ext.PriorityAutoKeep)
	} else {
		spn.SetTag(ext.SamplingPriority, ext.PriorityAutoReject)
	}
	spn.SetTag(keySamplingPriorityRate, rate)
}

type rulesSampler struct {
	rules   []rule
	limiter *rate.Limiter
	// "effective rate" calculations
	mu           sync.Mutex
	ts           int64
	allowed      int
	total        int
	previousRate float64
}

func newRulesSampler(config string) *rulesSampler {
	return &rulesSampler{
		rules:   parseRules(config),
		limiter: rate.NewLimiter(rate.Limit(100), 100),
	}
}

func (rs *rulesSampler) apply(span *span) bool {
	matched := false
	sr := 0.0
	for _, v := range rs.rules {
		if v.match(span) {
			matched = true
			sr = v.Rate
			break
		}
	}
	if !matched {
		return false
	}
	// rate sample
	span.SetTag("_dd.rule_psr", sr)
	if !sampledByRate(span.TraceID, sr) {
		span.SetTag(ext.SamplingPriority, ext.PriorityAutoReject)
		return true
	}
	// global rate limit and effective rate calculations
	defer rs.mu.Unlock()
	rs.mu.Lock()
	if ts := time.Now().Unix(); ts > rs.ts {
		// update "previous rate" and reset
		if ts-rs.ts == 1 && rs.total > 0 && rs.allowed > 0 {
			rs.previousRate = float64(rs.allowed) / float64(rs.total)
		} else {
			rs.previousRate = 0.0
		}
		rs.ts = ts
		rs.allowed = 0
		rs.total = 0
	}

	rs.total++
	// calculate effective rate, and tag the span
	er := (rs.previousRate + (float64(rs.allowed) / float64(rs.total))) / 2.0
	span.SetTag("_dd.limit_psr", er)
	if !rs.limiter.Allow() {
		span.SetTag(ext.SamplingPriority, ext.PriorityAutoReject)
		return true
	}
	span.SetTag(ext.SamplingPriority, ext.PriorityAutoKeep)
	rs.allowed++

	return true
}

type matchFunc func(string) bool

type rule struct {
	Service matchFunc
	Name    matchFunc
	Rate    float64
}

func (r *rule) match(s *span) bool {
	if r.Service != nil && !r.Service(s.Service) {
		return false
	}
	if r.Name != nil && !r.Name(s.Name) {
		return false
	}
	return true
}

// parseRules uses the rules config to produce a list of rules
// to apply during sampling. The initial implementation is simple
// preprocessing and lookups. It may be replaced with a proper parser
// if necessary in the future.
func parseRules(config string) []rule {
	// split the rules config into invididual rules
	tokens := strings.FieldsFunc(config, func(r rune) bool {
		switch r {
		case ';', '\n':
			return true
		default:
			return false
		}
	})
	// match functions for service and span name
	any := func(s string) bool {
		return true
	}
	equals := func(s string) matchFunc {
		return func(v string) bool {
			return s == v
		}
	}
	regex := func(r *regexp.Regexp) matchFunc {
		return func(v string) bool {
			return r.MatchString(v)
		}
	}
	optionalMatch := func(key string, line string) matchFunc {
		key = key + "="
		idx := strings.Index(line, key)
		if idx < 0 {
			return any
		}
		val := line[idx+len(key):]
		if idx := strings.Index(val, " "); idx >= 0 {
			val = val[:idx]
		}
		if val == "" {
			return any
		}
		re, err := regexp.Compile(val)
		if err != nil {
			return nil
		}
		if _, complete := re.LiteralPrefix(); complete {
			return equals(val)
		}
		return regex(re)
	}

	// each rule must have at least a valid rate, but the
	// span's service and name are optional
	var rules []rule
	for _, line := range tokens {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// extract rate (required)
		idx := strings.Index(line, "rate=")
		if idx < 0 {
			continue
		}
		rv := line[idx+len("rate="):]
		if idx := strings.Index(rv, " "); idx >= 0 {
			rv = rv[:idx]
		}
		rate, err := strconv.ParseFloat(rv, 64)
		if err != nil {
			continue
		}
		r := rule{
			Service: optionalMatch("service", line),
			Name:    optionalMatch("name", line),
			Rate:    rate,
		}
		rules = append(rules, r)
	}
	return rules
}
