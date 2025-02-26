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
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"golang.org/x/time/rate"
)

type provenance int32

const (
	Local    provenance = iota
	Customer provenance = 1
	Dynamic  provenance = 2
)

var provenances = []provenance{Local, Customer, Dynamic}

func (p provenance) String() string {
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

func (p provenance) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

func (p *provenance) UnmarshalJSON(data []byte) error {
	var prov string
	var err error
	if err = json.Unmarshal(data, &prov); err != nil {
		return err
	}
	if *p, err = parseProvenance(prov); err != nil {
		return err
	}
	return nil
}

func parseProvenance(p string) (provenance, error) {
	for _, v := range provenances {
		if strings.EqualFold(strings.TrimSpace(strings.ToLower(p)), v.String()) {
			return v, nil
		}
	}
	return Customer, fmt.Errorf("Invalid Provenance: \"%v\"", p)
}

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

	Provenance provenance

	ruleType SamplingRuleType
	limiter  *rateLimiter

	globRule *jsonRule
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
		globRule: &jsonRule{Service: service},
	}
}

// NameRule returns a SamplingRule that applies the provided sampling rate
// to spans that match the operation name provided.
func NameRule(name string, rate float64) SamplingRule {
	return SamplingRule{
		Name:     globMatch(name),
		ruleType: SamplingRuleTrace,
		Rate:     rate,
		globRule: &jsonRule{Name: name},
	}
}

// NameServiceRule returns a SamplingRule that applies the provided sampling rate
// to spans matching both the operation and service names provided.
func NameServiceRule(name string, service string, rate float64) SamplingRule {
	return SamplingRule{
		Service:  globMatch(service),
		Name:     globMatch(name),
		ruleType: SamplingRuleTrace,
		globRule: &jsonRule{Name: name, Service: service},
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
func TagsResourceRule(tags map[string]string, resource, name, service string, rate float64) SamplingRule {
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
		globRule: &jsonRule{Name: name, Service: service, Resource: resource, Tags: tags},
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
		globRule: &jsonRule{Name: name, Service: service, Resource: resource, Tags: tags},
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
		globRule: &jsonRule{Name: name, Service: service},
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
		globRule:     &jsonRule{Name: name, Service: service},
	}
}

// rateLimiter is a wrapper on top of golang.org/x/time/rate which implements a rate limiter but also
// returns the effective rate of allowance.
type rateLimiter struct {
	limiter *rate.Limiter

	prevTime time.Time // time at which prevAllowed and prevSeen were set
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
	if pattern == "" || pattern == "*" {
		return nil
	}
	// escaping regex characters
	pattern = regexp.QuoteMeta(pattern)
	// replacing '?' and '*' with regex characters
	pattern = strings.Replace(pattern, "\\?", ".", -1)
	pattern = strings.Replace(pattern, "\\*", ".*", -1)
	// pattern must match an entire string
	return regexp.MustCompile(fmt.Sprintf("(?i)^%s$", pattern))
}

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

type jsonRule struct {
	Service      string            `json:"service"`
	Name         string            `json:"name"`
	Rate         json.Number       `json:"sample_rate"`
	MaxPerSecond float64           `json:"max_per_second"`
	Resource     string            `json:"resource"`
	Tags         map[string]string `json:"tags"`
	Type         *SamplingRuleType `json:"type,omitempty"`
	Provenance   provenance        `json:"provenance,omitempty"`
}

func (j jsonRule) String() string {
	var s []string
	if j.Service != "" {
		s = append(s, fmt.Sprintf("Service:%s", j.Service))
	}
	if j.Name != "" {
		s = append(s, fmt.Sprintf("Name:%s", j.Name))
	}
	if j.Rate != "" {
		s = append(s, fmt.Sprintf("Rate:%s", j.Rate))
	}
	if j.MaxPerSecond != 0 {
		s = append(s, fmt.Sprintf("MaxPerSecond:%f", j.MaxPerSecond))
	}
	if j.Resource != "" {
		s = append(s, fmt.Sprintf("Resource:%s", j.Resource))
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

// unmarshalSamplingRules unmarshals JSON from b and returns the sampling rules found, attributing
// the type t to them. If any errors are occurred, they are returned.
func unmarshalSamplingRules(b []byte, spanType SamplingRuleType) ([]SamplingRule, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var jsonRules []jsonRule
	//	 if the JSON is an array, unmarshal it as an array of rules
	err := json.Unmarshal(b, &jsonRules)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON: %v", err)
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
			errs = append(
				errs,
				fmt.Sprintf("at index %d: ignoring rule %s: rate is out of [0.0, 1.0] range", i, v.String()),
			)
			continue
		}
		tagGlobs := make(map[string]*regexp.Regexp, len(v.Tags))
		for k, g := range v.Tags {
			tagGlobs[k] = globMatch(g)
		}
		rules = append(rules, SamplingRule{
			Service:      globMatch(v.Service),
			Name:         globMatch(v.Name),
			Rate:         rate,
			MaxPerSecond: v.MaxPerSecond,
			Resource:     globMatch(v.Resource),
			Tags:         tagGlobs,
			Provenance:   v.Provenance,
			ruleType:     spanType,
			limiter:      newSingleSpanRateLimiter(v.MaxPerSecond),
			globRule:     &jsonRules[i],
		})
	}
	if len(errs) != 0 {
		return rules, fmt.Errorf("%s", strings.Join(errs, "\n\t"))
	}
	return rules, nil
}

// MarshalJSON implements the json.Marshaler interface.
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
		log.Error("Error marshalling SamplingRule to json: %v", err)
	}
	return string(s)
}
