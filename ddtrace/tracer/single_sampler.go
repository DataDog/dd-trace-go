// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// singleSpanRulesSampler allows a user-defined list of rules to apply to spans
// to sample single spans.
// These rules match based on the span's Service and Name. If empty value is supplied
// to either Service or Name field, it will default to "*", allow all.
// When making a sampling decision, the rules are checked in order until
// a match is found.
// If a match is found, the rate from that rule is used.
// If no match is found, no changes or further sampling is applied to the spans.
// The rate is used to determine if the span should be sampled, but an upper
// limit can be defined using the max_per_second field when supplying the rule.
// If max_per_second is absent in the rule, the default is allow all.
// Its value is the max number of spans to sample per second.
// Spans that matched the rules but exceeded the rate limit are not sampled.
type singleSpanRulesSampler struct {
	rules []SamplingRule // the rules to match spans with
}

// newSingleSpanRulesSampler configures a *singleSpanRulesSampler instance using the given set of rules.
// Invalid rules or environment variable values are tolerated, by logging warnings and then ignoring them.
func newSingleSpanRulesSampler(rules []SamplingRule) *singleSpanRulesSampler {
	return &singleSpanRulesSampler{
		rules: rules,
	}
}

const (
	// spanSamplingMechanism specifies the sampling mechanism by which an individual span was sampled
	spanSamplingMechanism = "_dd.span_sampling.mechanism"

	// singleSpanSamplingMechanism specifies value reserved to indicate that a span was kept
	// on account of a single span sampling rule.
	singleSpanSamplingMechanism = 8

	// singleSpanSamplingRuleRate specifies the configured sampling probability for the single span sampling rule.
	singleSpanSamplingRuleRate = "_dd.span_sampling.rule_rate"

	// singleSpanSamplingMPS specifies the configured limit for the single span sampling rule
	// that the span matched. If there is no configured limit, then this tag is omitted.
	singleSpanSamplingMPS = "_dd.span_sampling.max_per_second"
)

// apply uses the sampling rules to determine the sampling rate for the
// provided span. If the rules don't match, then it returns false and the span is not
// modified.
func (rs *singleSpanRulesSampler) apply(span *span) bool {
	for _, rule := range rs.rules {
		if rule.match(span) {
			rs.applyRate(span, rule, rule.Rate, time.Now())
			return true
		}
	}
	return false
}

func (rs *singleSpanRulesSampler) applyRate(span *span, rule SamplingRule, rate float64, now time.Time) {
	span.setMetric(keyRulesSamplerAppliedRate, rate)
	if !sampledByRate(span.SpanID, rate) {
		span.setSamplingPriority(ext.PriorityUserReject, samplernames.RuleRate, rate)
		return
	}

	var sampled bool
	if rule.limiter != nil {
		sampled, rate = rule.limiter.allowOne(now)
		if !sampled {
			return
		}
	}
	span.setMetric(spanSamplingMechanism, singleSpanSamplingMechanism)
	span.setMetric(singleSpanSamplingRuleRate, rate)
	if rule.MaxPerSecond != 0 {
		span.setMetric(singleSpanSamplingMPS, rule.MaxPerSecond)
	}
}

// newSingleSpanRateLimiter returns a rate limiter which restricts the number of single spans sampled per second.
// This defaults to infinite, allow all behaviour. The MaxPerSecond value of the rule may override the default.
func newSingleSpanRateLimiter(mps float64) *rateLimiter {
	limit := math.MaxFloat64
	if mps > 0 {
		limit = mps
	}
	return &rateLimiter{
		limiter:  rate.NewLimiter(rate.Limit(limit), int(math.Ceil(limit))),
		prevTime: time.Now(),
	}
}

// globMatch compiles pattern string into glob format, i.e. regular expressions with only '?'
// and '*' treated as regex metacharacters.
func globMatch(pattern string) (*regexp.Regexp, error) {
	// escaping regex characters
	pattern = regexp.QuoteMeta(pattern)
	// replacing '?' and '*' with regex characters
	pattern = strings.Replace(pattern, "\\?", ".", -1)
	pattern = strings.Replace(pattern, "\\*", ".*", -1)
	//pattern must match an entire string
	return regexp.Compile(fmt.Sprintf("^%s$", pattern))
}
