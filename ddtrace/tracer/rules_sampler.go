// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"regexp"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/samplingrules"
)

// Type aliases and re-exports so that existing callers in ddtrace/tracer and
// external users of the public API continue to work without change.

type SamplingRule = samplingrules.SamplingRule
type SamplingRuleType = samplingrules.SamplingRuleType
type Rule = samplingrules.Rule
type Provenance = samplingrules.Provenance

const (
	SamplingRuleUndefined = samplingrules.SamplingRuleUndefined
	SamplingRuleTrace     = samplingrules.SamplingRuleTrace
	SamplingRuleSpan      = samplingrules.SamplingRuleSpan
	Local                 = samplingrules.Local
	Customer              = samplingrules.Customer
	Dynamic               = samplingrules.Dynamic
)

func TraceSamplingRules(rules ...samplingrules.Rule) []samplingrules.SamplingRule {
	return samplingrules.TraceSamplingRules(rules...)
}

func SpanSamplingRules(rules ...samplingrules.Rule) []samplingrules.SamplingRule {
	return samplingrules.SpanSamplingRules(rules...)
}

// EqualsFalseNegative tests whether two sets of rules are the same.
// The result may be false negative: true guarantees equality.
func EqualsFalseNegative(a, b []SamplingRule) bool {
	return samplingrules.EqualsFalseNegative(a, b)
}

// matchSamplingRule reports whether the span matches all criteria in the rule.
// +checklocksignore — Called from Finish() before s.finish(); span fields read-only at this point.
func matchSamplingRule(sr *SamplingRule, s *Span) bool {
	if sr.Service != nil && !sr.Service.MatchString(s.service) {
		return false
	}
	if sr.Name != nil && !sr.Name.MatchString(s.name) {
		return false
	}
	if sr.Resource != nil && !sr.Resource.MatchString(s.resource) {
		return false
	}
	if sr.Tags != nil {
		tagMatchers := make(map[string]func(string) bool, len(sr.Tags))
		for k, regex := range sr.Tags {
			if regex == nil {
				continue
			}
			r := regex
			tagMatchers[k] = func(v string) bool { return r.MatchString(v) }
		}
		if !s.matchTagsForSampling(tagMatchers) {
			return false
		}
	}
	return true
}

// rulesSampler holds instances of trace sampler and single span sampler configured with a given set of rules.
type rulesSampler struct {
	traces *traceRulesSampler
	spans  *singleSpanRulesSampler
}

// newRulesSampler configures a *rulesSampler instance using the given set of rules.
func newRulesSampler(traceRules, spanRules []SamplingRule, traceSampleRate, rateLimitPerSecond float64) *rulesSampler {
	return &rulesSampler{
		traces: newTraceRulesSampler(traceRules, traceSampleRate, rateLimitPerSecond),
		spans:  newSingleSpanRulesSampler(spanRules),
	}
}

func (r *rulesSampler) SampleTrace(s *Span) bool {
	if s == nil {
		return false
	}
	return r.traces.sampleRules(s)
}

func (r *rulesSampler) SampleTraceGlobalRate(s *Span) bool {
	if s == nil {
		return false
	}
	return r.traces.sampleGlobalRate(s)
}

func (r *rulesSampler) SampleSpan(s *Span) bool {
	if s == nil {
		return false
	}
	return r.spans.apply(s)
}

func (r *rulesSampler) HasSpanRules() bool { return r.spans.enabled() }

func (r *rulesSampler) TraceRateLimit() (float64, bool) { return r.traces.limit() }

// traceRulesSampler applies user-defined rules to traces and enforces a global rate limit.
type traceRulesSampler struct {
	mu         locking.RWMutex
	rules      []SamplingRule
	globalRate float64
	limiter    *samplingrules.RateLimiter
}

func newTraceRulesSampler(rules []SamplingRule, traceSampleRate, rateLimitPerSecond float64) *traceRulesSampler {
	return &traceRulesSampler{
		rules:      rules,
		globalRate: traceSampleRate,
		limiter:    samplingrules.NewRateLimiter(rateLimitPerSecond),
	}
}

func (rs *traceRulesSampler) enabled() bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.rules) > 0 || !math.IsNaN(rs.globalRate)
}

func (rs *traceRulesSampler) setGlobalSampleRate(rate float64) bool {
	if rate < 0.0 || rate > 1.0 {
		log.Warn("Ignoring trace sample rate %f: value out of range [0,1]", rate)
		return false
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if math.IsNaN(rs.globalRate) && math.IsNaN(rate) {
		return false
	}
	if rs.globalRate == rate {
		return false
	}
	rs.globalRate = rate
	return true
}

func (rs *traceRulesSampler) setTraceSampleRules(rules []SamplingRule) bool {
	if EqualsFalseNegative(rs.rules, rules) {
		return false
	}
	rs.rules = rules
	return true
}

func (rs *traceRulesSampler) sampleGlobalRate(span *Span) bool {
	if !rs.enabled() {
		return false
	}
	rs.mu.RLock()
	rate := rs.globalRate
	rs.mu.RUnlock()
	if math.IsNaN(rate) {
		return false
	}
	rs.applyRate(span, rate, time.Now(), samplernames.RuleRate)
	return true
}

func (rs *traceRulesSampler) sampleRules(span *Span) bool {
	if !rs.enabled() {
		return false
	}
	var matched bool
	rs.mu.RLock()
	rate := rs.globalRate
	rs.mu.RUnlock()
	sampler := samplernames.RuleRate
	for _, rule := range rs.rules {
		if matchSamplingRule(&rule, span) {
			matched = true
			rate = rule.Rate
			switch rule.Provenance {
			case Customer:
				sampler = samplernames.RemoteUserRule
			case Dynamic:
				sampler = samplernames.RemoteDynamicRule
			}
			break
		}
	}
	if !matched {
		return false
	}
	rs.applyRate(span, rate, time.Now(), sampler)
	return true
}

func (rs *traceRulesSampler) applyRate(span *Span, rate float64, now time.Time, sampler samplernames.SamplerName) {
	var limiter *samplingrules.RateLimiter
	if rs != nil {
		limiter = rs.limiter
	}
	span.applyTraceRuleSampling(rate, sampler, limiter, now)
}

func (rs *traceRulesSampler) limit() (float64, bool) {
	if rs.enabled() {
		return rs.limiter.Limit(), true
	}
	return math.NaN(), false
}

// singleSpanRulesSampler applies user-defined rules to individual spans.
type singleSpanRulesSampler struct {
	rules []SamplingRule
}

func newSingleSpanRulesSampler(rules []SamplingRule) *singleSpanRulesSampler {
	return &singleSpanRulesSampler{rules: rules}
}

func (rs *singleSpanRulesSampler) enabled() bool {
	return len(rs.rules) > 0
}

// +checklocksignore — Called on finished spans during trace flushing.
func (rs *singleSpanRulesSampler) apply(span *Span) bool {
	for _, rule := range rs.rules {
		if matchSamplingRule(&rule, span) {
			rate := rule.Rate
			if !sampledByRate(span.spanID, rate) {
				return false
			}
			sampled, rate := rule.AllowOne(nowTime())
			if !sampled {
				return false
			}
			span.applySingleSpanSamplingWithLock(rate, rule.MaxPerSecond)
			return true
		}
	}
	return false
}

// convertRemoteSamplingRules converts RC-received sampling rules into SamplingRule values.
func convertRemoteSamplingRules(rules *[]rcSamplingRule) *[]SamplingRule {
	if rules == nil {
		return nil
	}
	var converted []SamplingRule
	for _, rule := range *rules {
		var tagGlobs map[string]*regexp.Regexp
		var tagStrs map[string]string
		if rule.Tags != nil {
			tagGlobs = make(map[string]*regexp.Regexp, len(rule.Tags))
			tagStrs = make(map[string]string, len(rule.Tags))
			for _, tag := range rule.Tags {
				tagGlobs[tag.Key] = samplingrules.GlobMatch(tag.ValueGlob)
				tagStrs[tag.Key] = tag.ValueGlob
			}
		}
		converted = append(converted, samplingrules.NewRCSamplingRule(
			rule.Service, rule.Name, rule.Resource,
			rule.SampleRate, rule.Provenance,
			tagGlobs, tagStrs,
		))
	}
	return &converted
}
