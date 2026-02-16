// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package samplernames

// SamplerName specifies the name of a sampler which was
// responsible for a certain sampling decision.
type SamplerName int8

const (
	// Unknown specifies that the span was sampled
	// but, the tracer was unable to identify the sampler.
	// No sampling decision maker will be propagated.
	Unknown SamplerName = -1
	// Default specifies that the span was sampled without any sampler.
	Default SamplerName = 0
	// AgentRate specifies that the span was sampled
	// with a rate calculated by the trace agent.
	AgentRate SamplerName = 1
	// RemoteRate specifies that the span was sampled
	// with a dynamically calculated remote rate.
	RemoteRate SamplerName = 2
	// RuleRate specifies that the span was sampled by the local RuleSampler.
	RuleRate SamplerName = 3
	// Manual specifies that the span was sampled manually by user.
	Manual SamplerName = 4
	// AppSec specifies that the span was sampled by AppSec.
	AppSec SamplerName = 5
	// RemoteUserRate specifies that the span was sampled
	// with a user specified remote rate.
	RemoteUserRate SamplerName = 6
	// SingleSpan specifies that the span was sampled by single
	// span sampling rules.
	SingleSpan SamplerName = 8
	// Sampler name 9 is reserved/used by OTel ingestion.
	// Sampler name 10 is reserved for Data jobs (spark, databricks etc)
	// RemoteUserRule specifies that the span was sampled by a rule the user configured remotely
	// through Datadog UI.
	RemoteUserRule SamplerName = 11
	// RemoteDynamicRule specifies that the span was sampled by a rule configured by Datadog
	// Dynamic Sampling.
	RemoteDynamicRule SamplerName = 12
)

// Precomputed decision maker strings for each sampler name
var samplerStrings = map[SamplerName]string{
	Unknown:           "--1",
	Default:           "-0",
	AgentRate:         "-1",
	RemoteRate:        "-2",
	RuleRate:          "-3",
	Manual:            "-4",
	AppSec:            "-5",
	RemoteUserRate:    "-6",
	SingleSpan:        "-8",
	RemoteUserRule:    "-11",
	RemoteDynamicRule: "-12",
}

// DecisionMaker returns the decision maker representation of the sampler name.
// It returns the numeric value prefixed with "-" (e.g., "-1", "-2").
func (s SamplerName) DecisionMaker() string {
	if str, ok := samplerStrings[s]; ok {
		return str
	}
	// Fallback for unknown values (shouldn't happen in normal usage)
	// Return Unknown
	return "--1"
}
