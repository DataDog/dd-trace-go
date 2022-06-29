// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"golang.org/x/time/rate"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
	"math"
	"time"
)

// singleSpanRulesSampler allows a user-defined list of rules to apply to spans
// to sample single spans.
// These rules match based on the span's Service and Name. If empty value is supplied
// to either Service or Name field, it will default to "*", allow all
// When making a sampling decision, the rules are checked in order until
// a match is found.
// If a match is found, the rate from that rule is used.
// If no match is found, no changes or further sampling is applied to the spans.
// The rate is used to determine if the span should be sampled, but an upper
// limit can be defined using the max_per_second field when supplying the rule.
// If such value is absent in the rule, the default is allow all.
// Its value is the max number of spans to sample per second.
// Spans that matched the rules but exceeded the rate limit are not sampled.
type singleSpanRulesSampler struct {
	rules []SamplingRule // the rules to match spans with
}

// newRulesSampler configures a *rulesSampler instance using the given set of rules.
// Invalid rules or environment variable values are tolerated, by logging warnings and then ignoring them.
func newSingleSpanRulesSampler(rules []SamplingRule) *singleSpanRulesSampler {
	return &singleSpanRulesSampler{
		rules: rules,
	}
}

// apply uses the sampling rules to determine the sampling rate for the
// provided span. If the rules don't match, then it returns false and the span is not
// modified.
func (rs *singleSpanRulesSampler) apply(span *span) bool {
	var matched bool
	for _, rule := range rs.rules {
		if rule.match(span) {
			matched = true
			rs.applyRate(span, rule, rule.Rate, time.Now())
			break
		}
	}
	return matched
}

func (rs *singleSpanRulesSampler) applyRate(span *span, rule SamplingRule, rate float64, now time.Time) {
	span.SetTag(keyRulesSamplerAppliedRate, rate)
	if !sampledByRate(span.SpanID, rate) {
		span.setSamplingPriority(ext.PriorityUserReject, samplernames.RuleRate, rate)
		return
	}

	sampled := true
	if rule.limiter != nil {
		sampled, rate = rule.limiter.allowOne(now)
		if !sampled {
			return
		}
	}
	span.setMetric(ext.SpanSamplingMechanism, ext.SingleSpanSamplingMechanism)
	span.setMetric(ext.SingleSpanSamplingRuleRate, rate)
	if rule.MaxPerSecond != 0 {
		span.setMetric(ext.SingleSpanSamplingMPS, rule.MaxPerSecond)
	}
}

// newRateLimiter returns a rate limiter which restricts the number of single spans sampled per second.
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
