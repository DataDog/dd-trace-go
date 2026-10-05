// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package samplingrules defines the types and parsing logic for trace and span
// sampling rules. It is shared between ddtrace/tracer (runtime sampling) and
// internal/config (configuration ownership).
package samplingrules

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// Provenance describes the source of a sampling rule received via Remote Config.
type Provenance int32

const (
	Local    Provenance = 0
	Customer Provenance = 1
	Dynamic  Provenance = 2
)

var provenances = []Provenance{Local, Customer, Dynamic}

func (p Provenance) String() string {
	switch p {
	case Local:
		return "local"
	case Customer:
		return "customer"
	case Dynamic:
		return "dynamic"
	default:
		return ""
	}
}

func (p Provenance) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

func (p *Provenance) UnmarshalJSON(data []byte) error {
	var prov string
	if err := json.Unmarshal(data, &prov); err != nil {
		return err
	}
	var err error
	if *p, err = parseProvenance(prov); err != nil {
		return err
	}
	return nil
}

func parseProvenance(p string) (Provenance, error) {
	for _, v := range provenances {
		if strings.EqualFold(strings.TrimSpace(strings.ToLower(p)), v.String()) {
			return v, nil
		}
	}
	return Customer, fmt.Errorf("invalid provenance: %q", p)
}

// SamplingRuleType represents a type of sampling rule spans are matched against.
type SamplingRuleType int

const (
	SamplingRuleUndefined SamplingRuleType = 0
	SamplingRuleTrace     SamplingRuleType = 1
	SamplingRuleSpan      SamplingRuleType = 2
)

func (sr SamplingRuleType) String() string {
	switch sr {
	case SamplingRuleTrace:
		return "trace"
	case SamplingRuleSpan:
		return "span"
	default:
		return ""
	}
}

// Rule is used to create a SamplingRule via TraceSamplingRules or SpanSamplingRules.
type Rule struct {
	ServiceGlob  string
	NameGlob     string
	ResourceGlob string
	Tags         map[string]string
	Rate         float64
	MaxPerSecond float64
}

// SamplingRule is used for applying sampling rates to spans that match
// the service name, operation name or both.
// For basic usage, consider using the helper functions TraceSamplingRules and SpanSamplingRules.
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

	Provenance Provenance

	ruleType SamplingRuleType
	limiter  *RateLimiter
	globRule *jsonRule
}

// RuleType returns the rule's type (trace or span).
func (sr *SamplingRule) RuleType() SamplingRuleType {
	return sr.ruleType
}

// AllowOne applies the per-rule rate limiter. Returns whether the span is allowed
// and the effective rate. If no limiter is set, it always allows.
func (sr *SamplingRule) AllowOne(now time.Time) (bool, float64) {
	if sr.limiter == nil {
		return true, 1.0
	}
	return sr.limiter.AllowOne(now)
}

// EqualsFalseNegative compares two SamplingRules. The result may be false
// negative: true guarantees equality; false does not guarantee inequality.
func (sr *SamplingRule) EqualsFalseNegative(other *SamplingRule) bool {
	if (sr == nil) != (other == nil) {
		return false
	}
	if sr == nil {
		return true
	}
	if sr.Rate != other.Rate || sr.ruleType != other.ruleType ||
		!regexEqualsFalseNegative(sr.Service, other.Service) ||
		!regexEqualsFalseNegative(sr.Name, other.Name) ||
		!regexEqualsFalseNegative(sr.Resource, other.Resource) ||
		len(sr.Tags) != len(other.Tags) {
		return false
	}
	for k, v := range sr.Tags {
		if vo, ok := other.Tags[k]; !ok || !regexEqualsFalseNegative(v, vo) {
			return false
		}
	}
	return true
}

// EqualsFalseNegative tests whether two slices of SamplingRules are the same.
// The result may be false negative: true guarantees equality.
func EqualsFalseNegative(a, b []SamplingRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i, r := range a {
		if !r.EqualsFalseNegative(&b[i]) {
			return false
		}
	}
	return true
}

// SplitByType partitions a mixed slice of rules into trace rules and span rules.
func SplitByType(rules []SamplingRule) (trace, span []SamplingRule) {
	for _, r := range rules {
		if r.ruleType == SamplingRuleSpan {
			span = append(span, r)
		} else {
			trace = append(trace, r)
		}
	}
	return
}

// TraceSamplingRules creates sampling rules that apply to the entire trace if
// any spans satisfy the criteria.
func TraceSamplingRules(rules ...Rule) []SamplingRule {
	samplingRules := make([]SamplingRule, 0, len(rules))
	typ := SamplingRuleTrace
	for _, r := range rules {
		sr := SamplingRule{
			Service:  GlobMatch(r.ServiceGlob),
			Name:     GlobMatch(r.NameGlob),
			Resource: GlobMatch(r.ResourceGlob),
			Rate:     r.Rate,
			ruleType: SamplingRuleTrace,
			globRule: &jsonRule{
				Service:      r.ServiceGlob,
				Name:         r.NameGlob,
				Rate:         json.Number(strconv.FormatFloat(r.Rate, 'f', -1, 64)),
				MaxPerSecond: r.MaxPerSecond,
				Resource:     r.ResourceGlob,
				Tags:         r.Tags,
				Type:         &typ,
			},
		}
		if len(r.Tags) != 0 {
			sr.Tags = make(map[string]*regexp.Regexp, len(r.Tags))
			for k, v := range r.Tags {
				if g := GlobMatch(v); g != nil {
					sr.Tags[k] = g
				}
			}
		}
		samplingRules = append(samplingRules, sr)
	}
	return samplingRules
}

// SpanSamplingRules creates sampling rules that apply to individual spans
// without affecting the trace sampling decision.
func SpanSamplingRules(rules ...Rule) []SamplingRule {
	samplingRules := make([]SamplingRule, 0, len(rules))
	typ := SamplingRuleSpan
	for _, r := range rules {
		sr := SamplingRule{
			Service:      GlobMatch(r.ServiceGlob),
			Name:         GlobMatch(r.NameGlob),
			Resource:     GlobMatch(r.ResourceGlob),
			Rate:         r.Rate,
			ruleType:     SamplingRuleSpan,
			MaxPerSecond: r.MaxPerSecond,
			limiter:      NewSingleSpanRateLimiter(r.MaxPerSecond),
			globRule: &jsonRule{
				Service:      r.ServiceGlob,
				Name:         r.NameGlob,
				Rate:         json.Number(strconv.FormatFloat(r.Rate, 'f', -1, 64)),
				MaxPerSecond: r.MaxPerSecond,
				Resource:     r.ResourceGlob,
				Tags:         r.Tags,
				Type:         &typ,
			},
		}
		if len(r.Tags) != 0 {
			sr.Tags = make(map[string]*regexp.Regexp, len(r.Tags))
			for k, v := range r.Tags {
				if g := GlobMatch(v); g != nil {
					sr.Tags[k] = g
				}
			}
		}
		samplingRules = append(samplingRules, sr)
	}
	return samplingRules
}

// NewRCSamplingRule constructs a SamplingRule from a Remote Config update.
// tagGlobs maps tag keys to pre-compiled regexp patterns; tagStrs preserves
// the original glob strings for JSON serialisation. Pass nil tagStrs to omit tags.
func NewRCSamplingRule(service, name, resource string, rate float64, prov Provenance, tagGlobs map[string]*regexp.Regexp, tagStrs map[string]string) SamplingRule {
	gr := &jsonRule{Service: service, Name: name, Resource: resource, Tags: tagStrs}
	return SamplingRule{
		Service:    GlobMatch(service),
		Name:       GlobMatch(name),
		Resource:   GlobMatch(resource),
		Rate:       rate,
		Tags:       tagGlobs,
		Provenance: prov,
		globRule:   gr,
	}
}

// GlobMatch compiles a glob pattern into a regexp. Returns nil for "" or "*".
func GlobMatch(pattern string) *regexp.Regexp {
	if pattern == "" || pattern == "*" {
		return nil
	}
	pattern = regexp.QuoteMeta(pattern)
	pattern = strings.Replace(pattern, "\\?", ".", -1)
	pattern = strings.Replace(pattern, "\\*", ".*", -1)
	return regexp.MustCompile(fmt.Sprintf("(?i)^%s$", pattern))
}

// GlobsToRegexps compiles a map of glob-pattern strings to regexp patterns.
func GlobsToRegexps(globs map[string]string) map[string]*regexp.Regexp {
	result := make(map[string]*regexp.Regexp, len(globs))
	for k, g := range globs {
		result[k] = GlobMatch(g)
	}
	return result
}

// RegexEqualsFalseNegative reports whether two regexp patterns have the same string representation.
// True guarantees equality; false may be a false negative.
func RegexEqualsFalseNegative(a, b *regexp.Regexp) bool {
	return regexEqualsFalseNegative(a, b)
}

func regexEqualsFalseNegative(a, b *regexp.Regexp) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.String() == b.String()
}

type jsonRule struct {
	Service      string            `json:"service"`
	Name         string            `json:"name"`
	Rate         json.Number       `json:"sample_rate"`
	MaxPerSecond float64           `json:"max_per_second"`
	Resource     string            `json:"resource"`
	Tags         map[string]string `json:"tags"`
	Type         *SamplingRuleType `json:"type,omitempty"`
	Provenance   Provenance        `json:"provenance,omitempty"`
}

func (j jsonRule) String() string {
	var s []string
	if j.Service != "" {
		s = append(s, "Service:"+j.Service)
	}
	if j.Name != "" {
		s = append(s, "Name:"+j.Name)
	}
	if j.Rate != "" {
		s = append(s, fmt.Sprintf("Rate:%s", j.Rate))
	}
	if j.MaxPerSecond != 0 {
		s = append(s, fmt.Sprintf("MaxPerSecond:%f", j.MaxPerSecond))
	}
	if j.Resource != "" {
		s = append(s, "Resource:"+j.Resource)
	}
	if len(j.Tags) != 0 {
		s = append(s, fmt.Sprintf("Tags:%v", j.Tags))
	}
	if j.Type != nil {
		s = append(s, fmt.Sprintf("Type: %v", *j.Type))
	}
	if j.Provenance != Local {
		s = append(s, fmt.Sprintf("Provenance: %v", j.Provenance.String()))
	}
	return fmt.Sprintf("{%s}", strings.Join(s, " "))
}

// UnmarshalSamplingRules parses JSON sampling rules of the given type.
// On error, any rules that parsed successfully are still returned alongside
// the error — callers should log the error and use the partial result.
func UnmarshalSamplingRules(b []byte, spanType SamplingRuleType) ([]SamplingRule, error) {
	return unmarshalSamplingRules(b, spanType)
}

func unmarshalSamplingRules(b []byte, spanType SamplingRuleType) ([]SamplingRule, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var jsonRules []jsonRule
	if err := json.Unmarshal(b, &jsonRules); err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON: %s", err.Error())
	}
	return validateRules(jsonRules, spanType)
}

func validateRules(jsonRules []jsonRule, spanType SamplingRuleType) ([]SamplingRule, error) {
	var errs []string
	rules := make([]SamplingRule, 0, len(jsonRules))
	for i, v := range jsonRules {
		if v.Rate == "" {
			v.Rate = "1"
		}
		if v.Type != nil && *v.Type != spanType {
			spanType = *v.Type
		}
		rate, err := v.Rate.Float64()
		if err != nil {
			errs = append(errs, fmt.Sprintf("at index %d: %v", i, err))
			continue
		}
		if rate < 0.0 || rate > 1.0 {
			errs = append(errs, fmt.Sprintf("at index %d: ignoring rule %s: rate is out of [0.0, 1.0] range", i, v.String()))
			continue
		}
		tagGlobs := make(map[string]*regexp.Regexp, len(v.Tags))
		for k, g := range v.Tags {
			tagGlobs[k] = GlobMatch(g)
		}
		rules = append(rules, SamplingRule{
			Service:      GlobMatch(v.Service),
			Name:         GlobMatch(v.Name),
			Rate:         rate,
			MaxPerSecond: v.MaxPerSecond,
			Resource:     GlobMatch(v.Resource),
			Tags:         tagGlobs,
			Provenance:   v.Provenance,
			ruleType:     spanType,
			limiter:      NewSingleSpanRateLimiter(v.MaxPerSecond),
			globRule:     &jsonRules[i],
		})
	}
	if len(errs) != 0 {
		return rules, fmt.Errorf("%s", strings.Join(errs, "\n\t"))
	}
	return rules, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (sr *SamplingRule) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	var v jsonRule
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	rules, err := validateRules([]jsonRule{v}, SamplingRuleUndefined)
	if err != nil {
		return err
	}
	*sr = rules[0]
	return nil
}

// MarshalJSON implements json.Marshaler.
func (sr SamplingRule) MarshalJSON() ([]byte, error) {
	s := struct {
		Service      string            `json:"service,omitempty"`
		Name         string            `json:"name,omitempty"`
		Resource     string            `json:"resource,omitempty"`
		Rate         float64           `json:"sample_rate"`
		Tags         map[string]string `json:"tags,omitempty"`
		MaxPerSecond *float64          `json:"max_per_second,omitempty"`
		Provenance   string            `json:"provenance,omitempty"`
	}{}
	if sr.globRule != nil {
		s.Service = sr.globRule.Service
		s.Name = sr.globRule.Name
		s.Resource = sr.globRule.Resource
		s.Tags = sr.globRule.Tags
	} else {
		if sr.Service != nil {
			s.Service = sr.Service.String()
		}
		if sr.Name != nil {
			s.Name = sr.Name.String()
		}
		if sr.Resource != nil {
			s.Resource = sr.Resource.String()
		}
		s.Tags = make(map[string]string, len(sr.Tags))
		for k, v := range sr.Tags {
			if v != nil {
				s.Tags[k] = v.String()
			}
		}
	}
	if sr.MaxPerSecond != 0 {
		s.MaxPerSecond = &sr.MaxPerSecond
	}
	s.Rate = sr.Rate
	if sr.Provenance != Local {
		s.Provenance = sr.Provenance.String()
	}
	return json.Marshal(&s)
}

func (sr SamplingRule) String() string {
	s, err := sr.MarshalJSON()
	if err != nil {
		log.Error("Error marshalling SamplingRule to json: %s", err)
	}
	return string(s)
}

// RateLimiter is a token-bucket rate limiter that also tracks the effective
// allowance rate over the previous and current second.
type RateLimiter struct {
	Limiter *rate.Limiter

	mu          locking.Mutex
	PrevTime    time.Time // +checklocks:mu
	Allowed     float64   // +checklocks:mu
	Seen        float64   // +checklocks:mu
	PrevAllowed float64   // +checklocks:mu
	PrevSeen    float64   // +checklocks:mu
}

// AllowOne returns whether a single event is allowed and the effective rate.
func (r *RateLimiter) AllowOne(now time.Time) (bool, float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := now.Sub(r.PrevTime); d >= time.Second {
		if d.Truncate(time.Second) == time.Second && r.Seen > 0 {
			r.PrevAllowed = r.Allowed
			r.PrevSeen = r.Seen
		} else {
			r.PrevAllowed = 0
			r.PrevSeen = 0
		}
		r.PrevTime = now
		r.Allowed = 0
		r.Seen = 0
	}
	r.Seen++
	var sampled bool
	if r.Limiter.AllowN(now, 1) {
		r.Allowed++
		sampled = true
	}
	er := (r.PrevAllowed + r.Allowed) / (r.PrevSeen + r.Seen)
	return sampled, er
}

// Limit returns the configured rate limit in events per second.
func (r *RateLimiter) Limit() float64 {
	return float64(r.Limiter.Limit())
}

// NewRateLimiter returns a RateLimiter capped at ratePerSecond events.
func NewRateLimiter(ratePerSecond float64) *RateLimiter {
	return &RateLimiter{
		Limiter:  rate.NewLimiter(rate.Limit(ratePerSecond), int(math.Ceil(ratePerSecond))),
		PrevTime: time.Now(),
	}
}

// NewSingleSpanRateLimiter returns a RateLimiter for per-rule span rate limiting.
// A zero or negative mps means unlimited.
func NewSingleSpanRateLimiter(mps float64) *RateLimiter {
	limit := math.MaxFloat64
	if mps > 0 {
		limit = mps
	}
	return &RateLimiter{
		Limiter:  rate.NewLimiter(rate.Limit(limit), int(math.Ceil(limit))),
		PrevTime: time.Now(),
	}
}
