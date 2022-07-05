// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"os"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// traceRulesSampler allows a user-defined list of rules to apply to spans.
// These rules can match based on the span's Service, Name or both.
// When making a sampling decision, the rules are checked in order until
// a match is found.
// If a match is found, the rate from that rule is used.
// If no match is found, and the DD_TRACE_SAMPLE_RATE environment variable
// was set to a valid rate, that value is used.
// Otherwise, the rules sampler didn't apply to the span, and the decision
// is passed to the priority sampler.
//
// The rate is used to determine if the span should be sampled, but an upper
// limit can be defined using the DD_TRACE_RATE_LIMIT environment variable.
// Its value is the number of spans to sample per second.
// Spans that matched the rules but exceeded the rate limit are not sampled.
type traceRulesSampler struct {
	rules      []SamplingRule // the rules to match spans with
	globalRate float64        // a rate to apply when no rules match a span
	limiter    *rateLimiter   // used to limit the volume of spans sampled
}

// newTraceRulesSampler configures a *traceRulesSampler instance using the given set of rules.
// Invalid rules or environment variable values are tolerated, by logging warnings and then ignoring them.
func newTraceRulesSampler(rules []SamplingRule) *traceRulesSampler {
	return &traceRulesSampler{
		rules:      rules,
		globalRate: globalSampleRate(),
		limiter:    newRateLimiter(),
	}
}

func (rs *traceRulesSampler) enabled() bool {
	return len(rs.rules) > 0 || !math.IsNaN(rs.globalRate)
}

// apply uses the sampling rules to determine the sampling rate for the
// provided span. If the rules don't match, and a default rate hasn't been
// set using DD_TRACE_SAMPLE_RATE, then it returns false and the span is not
// modified.
func (rs *traceRulesSampler) apply(span *span) bool {
	if !rs.enabled() {
		// short path when disabled
		return false
	}

	var matched bool
	rate := rs.globalRate
	for _, rule := range rs.rules {
		if rule.match(span) {
			matched = true
			rate = rule.Rate
			break
		}
	}
	if !matched && math.IsNaN(rate) {
		// no matching rule or global rate, so we want to fall back
		// to priority sampling
		return false
	}

	rs.applyRate(span, rate, time.Now())
	return true
}

func (rs *traceRulesSampler) applyRate(span *span, rate float64, now time.Time) {
	span.SetTag(keyRulesSamplerAppliedRate, rate)
	if !sampledByRate(span.TraceID, rate) {
		span.setSamplingPriority(ext.PriorityUserReject, samplernames.RuleRate, rate)
		return
	}

	sampled, rate := rs.limiter.allowOne(now)
	if sampled {
		span.setSamplingPriority(ext.PriorityUserKeep, samplernames.RuleRate, rate)
	} else {
		span.setSamplingPriority(ext.PriorityUserReject, samplernames.RuleRate, rate)
	}
	span.SetTag(keyRulesSamplerLimiterRate, rate)
}

// limit returns the rate limit set in the rules sampler, controlled by DD_TRACE_RATE_LIMIT, and
// true if rules sampling is enabled. If not present it returns math.NaN() and false.
func (rs *traceRulesSampler) limit() (float64, bool) {
	if rs.enabled() {
		return float64(rs.limiter.limiter.Limit()), true
	}
	return math.NaN(), false
}

// defaultRateLimit specifies the default trace rate limit used when DD_TRACE_RATE_LIMIT is not set.
const defaultRateLimit = 100.0

// newRateLimiter returns a rate limiter which restricts the number of traces sampled per second.
// This defaults to 100.0. The DD_TRACE_RATE_LIMIT environment variable may override the default.
func newRateLimiter() *rateLimiter {
	limit := defaultRateLimit
	v := os.Getenv("DD_TRACE_RATE_LIMIT")
	if v != "" {
		l, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Warn("using default rate limit because DD_TRACE_RATE_LIMIT is invalid: %v", err)
		} else if l < 0.0 {
			log.Warn("using default rate limit because DD_TRACE_RATE_LIMIT is negative: %f", l)
		} else {
			// override the default limit
			limit = l
		}
	}
	return &rateLimiter{
		limiter:  rate.NewLimiter(rate.Limit(limit), int(math.Ceil(limit))),
		prevTime: time.Now(),
	}
}
