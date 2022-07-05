// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"

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
		spn.setSamplingPriority(ext.PriorityAutoKeep, samplernames.AgentRate, rate)
	} else {
		spn.setSamplingPriority(ext.PriorityAutoReject, samplernames.AgentRate, rate)
	}
	spn.SetTag(keySamplingPriorityRate, rate)
}

// rulesSampler holds instances of trace sampler and single span sampler, that are configured with the given set of rules.
// traceRulesSampler samples trace spans based on a user-defined set of rules and might impact sampling decision of the trace.
// singleSpanRulesSampler samples individual spans based on a separate user-defined set of rules and
// cannot impact the trace sampling decision.
type rulesSampler struct {
	traceRulesSampler      *traceRulesSampler
	singleSpanRulesSampler *singleSpanRulesSampler
}

// newRulesSampler configures a *rulesSampler instance using the given set of rules.
// Rules are split between trace and single span sampling rules according to their type.
// Such rules are user-defined through environment variable or WithSamplingRules option.
// Invalid rules or environment variable values are tolerated, by logging warnings and then ignoring them.
func newRulesSampler(rules []SamplingRule) *rulesSampler {
	var spanRules, traceRules []SamplingRule
	for _, rule := range rules {
		if rule.Type == SamplingRuleSingleSpan {
			rule.limiter = newSingleSpanRateLimiter(rule.MaxPerSecond)
			spanRules = append(spanRules, rule)
		} else {
			traceRules = append(traceRules, rule)
		}
	}
	return &rulesSampler{
		traceRulesSampler:      newTraceRulesSampler(traceRules),
		singleSpanRulesSampler: newSingleSpanRulesSampler(spanRules),
	}
}

const (
	// SamplingRuleTrace specifies that if a span matches a sampling rule of this type,
	// an entire trace might be kept based on the given span and sent to the agent.
	SamplingRuleTrace = iota
	// SamplingRuleSingleSpan specifies that if a span matches a sampling rule,
	// an individual span might be kept and sent to the agent.
	SamplingRuleSingleSpan
)

// samplingRulesFromEnv parses sampling rules from the DD_TRACE_SAMPLING_RULES,
// DD_SPAN_SAMPLING_RULES and DD_SPAN_SAMPLING_RULES_FILE environment variables.
func samplingRulesFromEnv() ([]SamplingRule, error) {
	var errs []string
	var rules []SamplingRule
	rulesFromEnv := os.Getenv("DD_TRACE_SAMPLING_RULES")
	if rulesFromEnv != "" {
		traceRules, err := processSamplingRules([]byte(rulesFromEnv), SamplingRuleTrace)
		if err != nil {
			errs = append(errs, err.Error())
		}
		rules = append(rules, traceRules...)
	}
	spanRules, err := spanSamplingRulesFromEnv()
	if err != nil {
		errs = append(errs, err.Error())
	}
	rules = append(rules, spanRules...)
	if len(errs) != 0 {
		return rules, fmt.Errorf("%s", strings.Join(errs, "\n\t"))
	}
	return rules, nil
}

// spanSamplingRulesFromEnv parses sampling rules from the DD_SPAN_SAMPLING_RULES and
// DD_SPAN_SAMPLING_RULES_FILE environment variables.
func spanSamplingRulesFromEnv() ([]SamplingRule, error) {
	var errs []string
	rulesFromEnv := os.Getenv("DD_SPAN_SAMPLING_RULES")
	if rulesFromEnv == "" {
		return nil, nil
	}
	rules, err := processSamplingRules([]byte(rulesFromEnv), SamplingRuleSingleSpan)
	if err != nil {
		errs = append(errs, err.Error())
	}
	rulesFile := os.Getenv("DD_SPAN_SAMPLING_RULES_FILE")
	if len(rules) != 0 {
		if rulesFile != "" {
			log.Warn("DIAGNOSTICS Error(s): DD_SPAN_SAMPLING_RULES is available and will take precedence over DD_SPAN_SAMPLING_RULES_FILE")
		}
		return rules, err
	}
	if rulesFile != "" {
		rulesFromEnvFile, err := os.ReadFile(rulesFile)
		if err != nil {
			log.Warn("Couldn't read file from DD_SPAN_SAMPLING_RULES_FILE")
			return nil, err
		}
		rules, err = processSamplingRules(rulesFromEnvFile, SamplingRuleSingleSpan)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) != 0 {
		return rules, fmt.Errorf("%s", strings.Join(errs, "\n\t"))
	}
	return rules, nil
}

func processSamplingRules(b []byte, spanType int) ([]SamplingRule, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var jsonRules []struct {
		Service      string      `json:"service"`
		Name         string      `json:"name"`
		Rate         json.Number `json:"sample_rate"`
		MaxPerSecond float64     `json:"max_per_second"`
	}
	err := json.Unmarshal(b, &jsonRules)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON: %v", err)
	}
	rules := make([]SamplingRule, 0, len(jsonRules))
	var errs []string
	for i, v := range jsonRules {
		if v.Rate == "" {
			errs = append(errs, fmt.Sprintf("at index %d: rate not provided", i))
			continue
		}
		rate, err := v.Rate.Float64()
		if err != nil {
			errs = append(errs, fmt.Sprintf("at index %d: %v", i, err))
			continue
		}
		if !(rate >= 0.0 && rate <= 1.0) {
			log.Warn("at index %d: ignoring rule %+v: rate is out of [0.0, 1.0] range", i, v)
			continue
		}
		if spanType == SamplingRuleSingleSpan {
			if v.Service == "" {
				v.Service = "*"
			}
			srvGlob, err := globMatch(v.Service)
			if err != nil {
				log.Warn("at index %d: ignoring rule %+v: service name regex pattern can't be compiled", i, v)
				continue
			}
			if v.Name == "" {
				v.Name = "*"
			}
			opGlob, err := globMatch(v.Name)
			if err != nil {
				log.Warn("at index %d: ignoring rule %+v: operation name regex pattern can't be compiled", i, v)
				continue
			}
			rules = append(rules, SamplingRule{
				Service:      srvGlob,
				Name:         opGlob,
				Rate:         rate,
				MaxPerSecond: v.MaxPerSecond,
				limiter:      newSingleSpanRateLimiter(v.MaxPerSecond),
				Type:         SamplingRuleSingleSpan,
			})
			continue
		}
		switch {
		case v.Service != "" && v.Name != "":
			rules = append(rules, NameServiceRule(v.Name, v.Service, rate))
		case v.Service != "":
			rules = append(rules, ServiceRule(v.Service, rate))
		case v.Name != "":
			rules = append(rules, NameRule(v.Name, rate))
		}
	}
	if len(errs) != 0 {
		return rules, fmt.Errorf("\n\t%s", strings.Join(errs, "\n\t"))
	}
	return rules, nil
}

// globalSampleRate returns the sampling rate found in the DD_TRACE_SAMPLE_RATE environment variable.
// If it is invalid or not within the 0-1 range, NaN is returned.
func globalSampleRate() float64 {
	defaultRate := math.NaN()
	v := os.Getenv("DD_TRACE_SAMPLE_RATE")
	if v == "" {
		return defaultRate
	}
	r, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Warn("ignoring DD_TRACE_SAMPLE_RATE: error: %v", err)
		return defaultRate
	}
	if r >= 0.0 && r <= 1.0 {
		return r
	}
	log.Warn("ignoring DD_TRACE_SAMPLE_RATE: out of range %f", r)
	return defaultRate
}

// SamplingRule is used for applying sampling rates to spans that match
// the service name, operation name or both.
// For basic usage, consider using the helper functions ServiceRule, NameRule, etc.
type SamplingRule struct {
	Service      *regexp.Regexp
	Name         *regexp.Regexp
	Rate         float64
	MaxPerSecond float64
	Type         int

	exactService string
	exactName    string
	limiter      *rateLimiter
}

// ServiceRule returns a SamplingRule that applies the provided sampling rate
// to spans that match the service name provided.
func ServiceRule(service string, rate float64) SamplingRule {
	return SamplingRule{
		exactService: service,
		Rate:         rate,
	}
}

// NameRule returns a SamplingRule that applies the provided sampling rate
// to spans that match the operation name provided.
func NameRule(name string, rate float64) SamplingRule {
	return SamplingRule{
		exactName: name,
		Rate:      rate,
	}
}

// NameServiceRule returns a SamplingRule that applies the provided sampling rate
// to spans matching both the operation and service names provided.
func NameServiceRule(name string, service string, rate float64) SamplingRule {
	return SamplingRule{
		exactService: service,
		exactName:    name,
		Rate:         rate,
	}
}

// RateRule returns a SamplingRule that applies the provided sampling rate to all spans.
func RateRule(rate float64) SamplingRule {
	return SamplingRule{
		Rate: rate,
	}
}

// match returns true when the span's details match all the expected values in the rule.
func (sr *SamplingRule) match(s *span) bool {
	if sr.Service != nil && !sr.Service.MatchString(s.Service) {
		return false
	} else if sr.exactService != "" && sr.exactService != s.Service {
		return false
	}
	if sr.Name != nil && !sr.Name.MatchString(s.Name) {
		return false
	} else if sr.exactName != "" && sr.exactName != s.Name {
		return false
	}
	return true
}

// MarshalJSON implements the json.Marshaler interface.
func (sr *SamplingRule) MarshalJSON() ([]byte, error) {
	s := struct {
		Service      string   `json:"service"`
		Name         string   `json:"name"`
		Rate         float64  `json:"sample_rate"`
		MaxPerSecond *float64 `json:"max_per_second,omitempty"`
	}{}
	if sr.exactService != "" {
		s.Service = sr.exactService
	} else if sr.Service != nil {
		s.Service = fmt.Sprintf("%s", sr.Service)
	}
	if sr.exactName != "" {
		s.Name = sr.exactName
	} else if sr.Name != nil {
		s.Name = fmt.Sprintf("%s", sr.Name)
	}
	s.Rate = sr.Rate
	if sr.MaxPerSecond != 0 {
		s.MaxPerSecond = &sr.MaxPerSecond
	}
	return json.Marshal(&s)
}

// rateLimiter is a wrapper on top of golang.org/x/time/rate which implements a rate limiter but also
// returns the effective rate of allowance.
type rateLimiter struct {
	limiter *rate.Limiter

	mu          sync.Mutex // guards below fields
	prevTime    time.Time  // time at which prevAllowed and prevSeen were set
	allowed     float64    // number of spans allowed in the current period
	seen        float64    // number of spans seen in the current period
	prevAllowed float64    // number of spans allowed in the previous period
	prevSeen    float64    // number of spans seen in the previous period
}

// allowOne returns the rate limiter's decision to allow the span to be sampled, and the
// effective rate at the time it is called. The effective rate is computed by averaging the rate
// for the previous second with the current rate
func (r *rateLimiter) allowOne(now time.Time) (bool, float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := now.Sub(r.prevTime); d >= time.Second {
		// enough time has passed to reset the counters
		if d.Truncate(time.Second) == time.Second && r.seen > 0 {
			// exactly one second, so update prev
			r.prevAllowed = r.allowed
			r.prevSeen = r.seen
		} else {
			// more than one second, so reset previous rate
			r.prevAllowed = 0
			r.prevSeen = 0
		}
		r.prevTime = now
		r.allowed = 0
		r.seen = 0
	}

	r.seen++
	var sampled bool
	if r.limiter.AllowN(now, 1) {
		r.allowed++
		sampled = true
	}
	er := (r.prevAllowed + r.allowed) / (r.prevSeen + r.seen)
	return sampled, er
}
