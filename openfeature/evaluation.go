// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"time"
)

const (
	defaultEvaluationFlushInterval = 10 * time.Second
	evaluationEndpoint             = "/evp_proxy/v2/api/v2/flagevaluation"
	defaultEvaluationPerFlagCap    = 10_000
	defaultEvaluationGlobalCap     = 65_536
	envEvaluationPerFlagCap        = "DD_FLAGGING_EVALUATION_PER_FLAG_CAP"
	envEvaluationGlobalCap         = "DD_FLAGGING_EVALUATION_MAP_CAPACITY"
	envEvaluationCountsEnabled     = "DD_FLAGGING_EVALUATION_COUNTS_ENABLED"
)

type evaluationFlag struct {
	Key string `json:"key"`
}

type evaluationVariant struct {
	Key string `json:"key"`
}

type evaluationAllocation struct {
	Key string `json:"key"`
}

type evaluationTargetingRule struct {
	Key string `json:"key"`
}

type evaluationError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type evaluationDDContext struct {
	Service string `json:"service,omitempty"`
	Version string `json:"version,omitempty"`
	Env     string `json:"env,omitempty"`
}

type evaluationEventContext struct {
	Evaluation map[string]any       `json:"evaluation,omitempty"`
	DD         *evaluationDDContext `json:"dd,omitempty"`
}

// evaluationEvent is a single flag-evaluation count event matching the browser SDK's FlagEvaluationEvent schema.
// Rollup events (per-flag cap exceeded) are distinguishable by the absence of targeting_key and context.evaluation.
type evaluationEvent struct {
	Flag               evaluationFlag           `json:"flag"`
	EvaluationCount    int64                    `json:"evaluation_count"`
	FirstEvaluation    int64                    `json:"first_evaluation"`
	LastEvaluation     int64                    `json:"last_evaluation"`
	Timestamp          int64                    `json:"timestamp"`
	RuntimeDefaultUsed bool                     `json:"runtime_default_used"`
	Reason             string                   `json:"reason,omitempty"`
	TargetingKey       string                   `json:"targeting_key,omitempty"`
	Variant            *evaluationVariant       `json:"variant,omitempty"`
	Allocation         *evaluationAllocation    `json:"allocation,omitempty"`
	TargetingRule      *evaluationTargetingRule `json:"targeting_rule,omitempty"`
	Error              *evaluationError         `json:"error,omitempty"`
	Context            *evaluationEventContext  `json:"context,omitempty"`
}

type evaluationPayload struct {
	Context         evaluationDDContext `json:"context"`
	FlagEvaluations []evaluationEvent   `json:"flagEvaluations"`
}
