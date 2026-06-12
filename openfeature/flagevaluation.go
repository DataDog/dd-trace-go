// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package openfeature provides flag evaluation EVP emission.
// This file contains signature-only stubs; the real implementation is in plan 02.
package openfeature

import (
	"net/http"
	"net/url"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	of "github.com/open-feature/go-sdk/openfeature"
)

const (
	// defaultFlagEvalFlushInterval is the flush interval for EVP flag evaluation events.
	// D-05: dedicated 10 s timer (contract value); separate from exposureWriter's 1 s interval.
	defaultFlagEvalFlushInterval = 10 * time.Second

	// flagEvaluationEndpoint is the EVP proxy endpoint for flag evaluation events.
	flagEvaluationEndpoint = "/evp_proxy/v2/api/v2/flagevaluations"

	// Context pruning limits (D-07) — mirror worker.ts MAX_EVALUATION_CONTEXT_FIELDS / MAX_FIELD_LENGTH.
	maxContextFields = 256
	maxFieldLength   = 256

	// Aggregation caps (D-08/D-09/CONT-10).
	defaultEvalGlobalCap   = 65_536 // bounds total distinct buckets across all tiers
	defaultEvalPerFlagCap  = 10_000 // bounds full-fidelity buckets per flag
	defaultEvalDegradedCap = 10_000 // bounds degraded map (absent from POC — Gate 8 fix)
)

// evaluationAggregationKey is a struct-keyed map key (collision-free for enumerable dims).
// contextHash is a uint64 FNV-1a hash of the pruned context — NOT the sole map key.
// Replaces the FNV-1a-keyed map from PR #4874 POC (CONT-05 / source_comment_id 3395004724).
type evaluationAggregationKey struct {
	flagKey          string
	variant          string
	allocationKey    string
	targetingRuleKey string
	reason           string
	targetingKey     string
	contextHash      uint64 // context is map[string]any (not comparable); hash is a discriminator only
}

// evaluationDegradedKey is the key for the degraded aggregation map.
// Drops targeting key, context, and targeting rule key relative to the full key.
type evaluationDegradedKey struct {
	flagKey       string
	variant       string
	allocationKey string
	reason        string
}

// evaluationUltraDegradedKey is the key for the ultra-degraded aggregation map.
// Contains only flag key + variant; bounded by flag×variant enumeration.
type evaluationUltraDegradedKey struct {
	flagKey string
	variant string
}

// evaluationEntry holds per-window counts and time bounds for one aggregation bucket.
type evaluationEntry struct {
	count           int64
	firstEvaluation int64 // milliseconds — min across all recordings in this window
	lastEvaluation  int64 // milliseconds — max across all recordings in this window
	runtimeDefault  bool
	// For full tier only:
	targetingKey string
	contextAttrs map[string]any
	errorMessage string
}

// flagEvaluationAggregator holds the three-tier aggregation maps.
type flagEvaluationAggregator struct {
	mu          sync.Mutex
	full        map[evaluationAggregationKey]*evaluationEntry
	degraded    map[evaluationDegradedKey]*evaluationEntry
	ultraDeg    map[evaluationUltraDegradedKey]*evaluationEntry
	perFlagFull map[string]int // flagKey → count of full-fidelity entries for this flag
	globalCount int
	globalCap   int
	perFlagCap  int
	degradedCap int
}

// flagEvaluationEvent matches flagevaluation.json — required fields always present;
// optional fields use omitempty (absent = schema-valid for degraded/ultra-degraded tiers).
type flagEvaluationEvent struct {
	Timestamp       int64                  `json:"timestamp"`
	Flag            flagEvalFlag           `json:"flag"`
	FirstEvaluation int64                  `json:"first_evaluation"`
	LastEvaluation  int64                  `json:"last_evaluation"`
	EvaluationCount int64                  `json:"evaluation_count"`
	RuntimeDefault  bool                   `json:"runtime_default_used,omitempty"`
	TargetingKey    string                 `json:"targeting_key,omitempty"`
	Variant         *flagEvalVariant       `json:"variant,omitempty"`
	Allocation      *flagEvalAllocation    `json:"allocation,omitempty"`
	TargetingRule   *flagEvalTargetingRule `json:"targeting_rule,omitempty"`
	Error           *flagEvalError         `json:"error,omitempty"`
	Context         *flagEvalEventContext  `json:"context,omitempty"`
}

// flagEvalFlag holds the flag key.
type flagEvalFlag struct{ Key string `json:"key"` }

// flagEvalVariant holds the variant key.
type flagEvalVariant struct{ Key string `json:"key"` }

// flagEvalAllocation holds the allocation key.
type flagEvalAllocation struct{ Key string `json:"key"` }

// flagEvalTargetingRule holds the targeting rule key.
type flagEvalTargetingRule struct{ Key string `json:"key"` }

// flagEvalError holds the error message.
type flagEvalError struct{ Message string `json:"message"` }

// flagEvalEventContext holds the per-event context object.
type flagEvalEventContext struct {
	Evaluation map[string]any `json:"evaluation,omitempty"`
	DD         *flagEvalContextDD `json:"dd,omitempty"`
}

// flagEvalContextDD holds the Datadog-specific context inside per-event context.dd.
type flagEvalContextDD struct {
	Service string `json:"service,omitempty"`
}

// flagEvaluationPayload matches batchedflagevaluations.json.
type flagEvaluationPayload struct {
	Context         flagEvalDDContext     `json:"context"`
	FlagEvaluations []flagEvaluationEvent `json:"flagEvaluations"`
}

// flagEvalDDContext carries service/env/version for the batch-level context.
type flagEvalDDContext struct {
	Service string `json:"service"`
	Env     string `json:"env,omitempty"`
	Version string `json:"version,omitempty"`
}

// flagEvaluationWriter manages aggregation and periodic flushing of EVP flag evaluation events.
type flagEvaluationWriter struct {
	aggregator    flagEvaluationAggregator
	flushInterval time.Duration
	httpClient    *http.Client
	agentURL      *url.URL
	ddContext     flagEvalDDContext // service/env/version — same source as exposureContext
	ticker        *time.Ticker
	stopChan      chan struct{}
	stopped       bool
	jsonConfig    jsoniter.API
}

// evalDetails holds extracted flag evaluation fields for EVP aggregation.
// Used only by flagEvaluationHook; does NOT replace extraction in flageval_metrics.go.
type evalDetails struct {
	flagKey          string
	variant          string
	reason           string
	allocationKey    string
	targetingRuleKey string
	targetingKey     string
	errorMessage     string
	runtimeDefault   bool
}

// newFlagEvaluationWriter creates a new flag evaluation writer.
// Plan 02 implements the body; this is a signature-only stub.
func newFlagEvaluationWriter(_ ProviderConfig) *flagEvaluationWriter {
	panic("not implemented")
}

// start begins the periodic flushing — called from InitWithContext(), NOT from the constructor.
// Plan 02 implements the body.
func (w *flagEvaluationWriter) start() {
	panic("not implemented")
}

// stop stops the flush ticker and marks the writer as stopped.
// Plan 02 implements the body.
func (w *flagEvaluationWriter) stop() {
	panic("not implemented")
}

// flush sends aggregated evaluation events to the Datadog Agent EVP proxy.
// Plan 02 implements the body.
func (w *flagEvaluationWriter) flush() {
	panic("not implemented")
}

// record adds an evaluation to the aggregation buffer.
// Plan 02 implements the body.
func (w *flagEvaluationWriter) record(_ of.HookContext, _ of.InterfaceEvaluationDetails) {
	panic("not implemented")
}

// add adds one evaluation observation to the aggregator.
// Plan 02 implements the body.
func (a *flagEvaluationAggregator) add(_ evalDetails, _ map[string]any, _ int64) {
	panic("not implemented")
}

// pruneContext applies 256-field / 256-char limits before buffering (D-07).
// Mirrors worker.ts MAX_EVALUATION_CONTEXT_FIELDS / MAX_FIELD_LENGTH exactly.
// Plan 02 implements the body.
func pruneContext(_ map[string]any) map[string]any {
	panic("not implemented")
}

// extractEvalDetails extracts EVP-relevant fields from hook context and evaluation details.
// Used only by flagEvaluationHook (flageval_metrics.go is NOT modified — D-06/PRES-01).
// Plan 02 implements the body.
func extractEvalDetails(_ of.HookContext, _ of.InterfaceEvaluationDetails) evalDetails {
	panic("not implemented")
}
