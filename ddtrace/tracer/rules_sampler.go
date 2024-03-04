// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"golang.org/x/time/rate"
)

// SamplingRule is used for applying sampling rates to spans that match
// the service name, operation name or both.
// For basic usage, consider using the helper functions ServiceRule, NameRule, etc.
type SamplingRule struct {
	// Service specifies the regex pattern that a span service name must match.
	Service *regexp.Regexp

	// Name specifies the regex pattern that a span operation name must match.
	Name *regexp.Regexp

	// Rate specifies the sampling rate that should be applied to spans that match
	// service and/or name of the rule.
	Rate float64

	// MaxPerSecond specifies max number of spans per second that can be sampled per the rule.
	// If not specified, the default is no limit.
	MaxPerSecond float64

	// Resource specifies the regex pattern that a span resource must match.
	Resource *regexp.Regexp

	// Tags specifies the map of key-value patterns that span tags must match.
	Tags map[string]*regexp.Regexp

	ruleType SamplingRuleType
	limiter  *rateLimiter
}

// SamplingRuleType represents a type of sampling rule spans are matched against.
type SamplingRuleType = v2.SamplingRuleType

const (
	SamplingRuleUndefined SamplingRuleType = 0

	// SamplingRuleTrace specifies a sampling rule that applies to the entire trace if any spans satisfy the criteria.
	// If a sampling rule is of type SamplingRuleTrace, such rule determines the sampling rate to apply
	// to trace spans. If a span matches that rule, it will impact the trace sampling decision.
	SamplingRuleTrace = v2.SamplingRuleTrace

	// SamplingRuleSpan specifies a sampling rule that applies to a single span without affecting the entire trace.
	// If a sampling rule is of type SamplingRuleSingleSpan, such rule determines the sampling rate to apply
	// to individual spans. If a span matches a rule, it will NOT impact the trace sampling decision.
	// In the case that a trace is dropped and thus not sent to the Agent, spans kept on account
	// of matching SamplingRuleSingleSpan rules must be conveyed separately.
	SamplingRuleSpan = v2.SamplingRuleSpan
)

// ServiceRule returns a SamplingRule that applies the provided sampling rate
// to spans that match the service name provided.
func ServiceRule(service string, rate float64) SamplingRule {
	return SamplingRule{
		Service:  globMatch(service),
		ruleType: SamplingRuleTrace,
		Rate:     rate,
	}
}

// NameRule returns a SamplingRule that applies the provided sampling rate
// to spans that match the operation name provided.
func NameRule(name string, rate float64) SamplingRule {
	return SamplingRule{
		Name:     globMatch(name),
		ruleType: SamplingRuleTrace,
		Rate:     rate,
	}
}

// NameServiceRule returns a SamplingRule that applies the provided sampling rate
// to spans matching both the operation and service names provided.
func NameServiceRule(name string, service string, rate float64) SamplingRule {
	return SamplingRule{
		Service:  globMatch(service),
		Name:     globMatch(name),
		ruleType: SamplingRuleTrace,
		Rate:     rate,
	}
}

// RateRule returns a SamplingRule that applies the provided sampling rate to all spans.
func RateRule(rate float64) SamplingRule {
	return SamplingRule{
		Rate:     rate,
		ruleType: SamplingRuleTrace,
	}
}

// TagsResourceRule returns a SamplingRule that applies the provided sampling rate to traces with spans that match
// resource, name, service and tags provided.
func TagsResourceRule(tags map[string]*regexp.Regexp, resource, name, service string, rate float64) SamplingRule {
	return SamplingRule{
		Service:  globMatch(service),
		Name:     globMatch(name),
		Resource: globMatch(resource),
		Rate:     rate,
		Tags:     tags,
		ruleType: SamplingRuleTrace,
	}
}

// SpanTagsResourceRule returns a SamplingRule that applies the provided sampling rate to spans that match
// resource, name, service and tags provided. Values of the tags map are expected to be in glob format.
func SpanTagsResourceRule(tags map[string]string, resource, name, service string, rate float64) SamplingRule {
	globTags := make(map[string]*regexp.Regexp, len(tags))
	for k, v := range tags {
		if g := globMatch(v); g != nil {
			globTags[k] = g
		}
	}
	return SamplingRule{
		Service:  globMatch(service),
		Name:     globMatch(name),
		Resource: globMatch(resource),
		Rate:     rate,
		Tags:     globTags,
		ruleType: SamplingRuleSpan,
	}
}

// SpanNameServiceRule returns a SamplingRule of type SamplingRuleSpan that applies
// the provided sampling rate to all spans matching the operation and service name glob patterns provided.
// Operation and service fields must be valid glob patterns.
func SpanNameServiceRule(name, service string, rate float64) SamplingRule {
	return SamplingRule{
		Service:  globMatch(service),
		Name:     globMatch(name),
		Rate:     rate,
		ruleType: SamplingRuleSpan,
		limiter:  newSingleSpanRateLimiter(0),
	}
}

// SpanNameServiceMPSRule returns a SamplingRule of type SamplingRuleSpan that applies
// the provided sampling rate to all spans matching the operation and service name glob patterns
// up to the max number of spans per second that can be sampled.
// Operation and service fields must be valid glob patterns.
func SpanNameServiceMPSRule(name, service string, rate, limit float64) SamplingRule {
	return SamplingRule{
		Service:      globMatch(service),
		Name:         globMatch(name),
		MaxPerSecond: limit,
		Rate:         rate,
		ruleType:     SamplingRuleSpan,
		limiter:      newSingleSpanRateLimiter(limit),
	}
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
func globMatch(pattern string) *regexp.Regexp {
	if pattern == "" {
		return nil
	}
	// escaping regex characters
	pattern = regexp.QuoteMeta(pattern)
	// replacing '?' and '*' with regex characters
	pattern = strings.Replace(pattern, "\\?", ".", -1)
	pattern = strings.Replace(pattern, "\\*", ".*", -1)
	// pattern must match an entire string
	return regexp.MustCompile(fmt.Sprintf("^%s$", pattern))
}

// MarshalJSON implements the json.Marshaler interface.
func (sr *SamplingRule) MarshalJSON() ([]byte, error) {
	s := struct {
		Service      string            `json:"service,omitempty"`
		Name         string            `json:"name,omitempty"`
		Resource     string            `json:"resource,omitempty"`
		Rate         float64           `json:"sample_rate"`
		Tags         map[string]string `json:"tags,omitempty"`
		Type         *string           `json:"type,omitempty"`
		MaxPerSecond *float64          `json:"max_per_second,omitempty"`
	}{}
	if sr.Service != nil {
		s.Service = sr.Service.String()
	}
	if sr.Name != nil {
		s.Name = sr.Name.String()
	}
	if sr.MaxPerSecond != 0 {
		s.MaxPerSecond = &sr.MaxPerSecond
	}
	if sr.Resource != nil {
		s.Resource = sr.Resource.String()
	}
	s.Rate = sr.Rate
	if v := sr.ruleType.String(); v != "" {
		t := fmt.Sprintf("%v(%d)", v, sr.ruleType)
		s.Type = &t
	}
	s.Tags = make(map[string]string, len(sr.Tags))
	for k, v := range sr.Tags {
		if v != nil {
			s.Tags[k] = v.String()
		}
	}
	return json.Marshal(&s)
}
