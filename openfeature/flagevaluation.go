// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"

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
	defaultEvalGlobalCap   = 65_536 // bounds full-tier buckets only; degraded/ultra are bounded separately (WR-02)
	defaultEvalPerFlagCap  = 10_000 // bounds full-fidelity buckets per flag
	defaultEvalDegradedCap = 10_000 // bounds degraded map (absent from POC — Gate 8 fix)
)

// evaluationAggregationKey is a struct-keyed map key (collision-free for enumerable dims).
// contextHash is a uint64 FNV-1a hash of the pruned context — NOT the sole map key.
// Replaces the FNV-1a-keyed map from PR #4874 POC (CONT-05 / source_comment_id 3395004724).
type evaluationAggregationKey struct {
	flagKey       string
	variant       string
	allocationKey string
	reason        string
	targetingKey  string
	contextHash   uint64 // context is map[string]any (not comparable); hash is a discriminator only
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
	Timestamp       int64                 `json:"timestamp"`
	Flag            flagEvalFlag          `json:"flag"`
	FirstEvaluation int64                 `json:"first_evaluation"`
	LastEvaluation  int64                 `json:"last_evaluation"`
	EvaluationCount int64                 `json:"evaluation_count"`
	RuntimeDefault  bool                  `json:"runtime_default_used,omitempty"`
	TargetingKey    string                `json:"targeting_key,omitempty"`
	Variant         *flagEvalVariant      `json:"variant,omitempty"`
	Allocation      *flagEvalAllocation   `json:"allocation,omitempty"`
	Error           *flagEvalError        `json:"error,omitempty"`
	Context         *flagEvalEventContext `json:"context,omitempty"`
}

// flagEvalFlag holds the flag key.
type flagEvalFlag struct {
	Key string `json:"key"`
}

// flagEvalVariant holds the variant key.
type flagEvalVariant struct {
	Key string `json:"key"`
}

// flagEvalAllocation holds the allocation key.
type flagEvalAllocation struct {
	Key string `json:"key"`
}

// flagEvalError holds the error message.
type flagEvalError struct {
	Message string `json:"message"`
}

// flagEvalEventContext holds the per-event context object.
type flagEvalEventContext struct {
	Evaluation map[string]any     `json:"evaluation,omitempty"`
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
	flagKey        string
	variant        string
	reason         string
	allocationKey  string
	targetingKey   string
	errorMessage   string
	runtimeDefault bool
}

// newFlagEvaluationWriter creates a new flag evaluation writer.
// The writer uses the same HTTP transport setup as exposure.go (D-04).
func newFlagEvaluationWriter(config ProviderConfig) *flagEvaluationWriter {
	agentURL := internal.AgentURLFromEnv()
	var httpClient *http.Client
	if agentURL.Scheme == "unix" {
		httpClient = internal.UDSClient(agentURL.Path, defaultHTTPTimeout)
		agentURL = internal.UnixDataSocketURL(agentURL.Path)
	} else {
		httpClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
	}

	executable, _ := os.Executable()

	flushInterval := cmp.Or(config.FlagEvaluationFlushInterval, defaultFlagEvalFlushInterval)

	return &flagEvaluationWriter{
		flushInterval: flushInterval,
		httpClient:    httpClient,
		agentURL:      agentURL,
		stopChan:      make(chan struct{}),
		jsonConfig:    jsoniter.Config{}.Froze(),
		ddContext: flagEvalDDContext{
			Service: cmp.Or(env.Get("DD_SERVICE"), globalconfig.ServiceName(), executable),
			Version: env.Get("DD_VERSION"),
			Env:     env.Get("DD_ENV"),
		},
		aggregator: flagEvaluationAggregator{
			full:        make(map[evaluationAggregationKey]*evaluationEntry),
			degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
			ultraDeg:    make(map[evaluationUltraDegradedKey]*evaluationEntry),
			perFlagFull: make(map[string]int),
			globalCap:   defaultEvalGlobalCap,
			perFlagCap:  defaultEvalPerFlagCap,
			degradedCap: defaultEvalDegradedCap,
		},
	}
}

// start begins the periodic flushing — called from InitWithContext(), NOT from the constructor.
// Mirrors exposure.go's start() goroutine + panic recovery pattern.
func (w *flagEvaluationWriter) start() {
	w.ticker = time.NewTicker(w.flushInterval)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("openfeature: flag evaluation writer recovered panic: %s", r)
				var errAttr slog.Attr
				if err, ok := r.(error); ok {
					errAttr = slog.Any("panic", telemetrylog.NewSafeError(err))
				} else {
					errAttr = slog.Any("panic", r)
				}
				telemetrylog.Error("openfeature: flag evaluation writer recovered panic", errAttr)
			}
			w.stop()
		}()

		for {
			select {
			case <-w.ticker.C:
				w.flush()
			case <-w.stopChan:
				return
			}
		}
	}()
}

// stop stops the flush ticker and marks the writer as stopped.
func (w *flagEvaluationWriter) stop() {
	w.aggregator.mu.Lock()
	if w.stopped {
		w.aggregator.mu.Unlock()
		return
	}
	w.stopped = true
	w.aggregator.mu.Unlock()

	// Signal the goroutine to stop
	close(w.stopChan)

	// Stop the ticker
	if w.ticker != nil {
		w.ticker.Stop()
	}

	log.Debug("openfeature: flag evaluation writer stopped")
}

// flush drains the aggregator, assembles per-tier events, and sends them to the agent.
func (w *flagEvaluationWriter) flush() {
	w.aggregator.mu.Lock()

	// Under lock: drain all three maps.
	full := w.aggregator.full
	degraded := w.aggregator.degraded
	ultraDeg := w.aggregator.ultraDeg
	stopped := w.stopped

	if (len(full)+len(degraded)+len(ultraDeg)) == 0 || stopped {
		w.aggregator.mu.Unlock()
		return
	}

	// Reset maps.
	w.aggregator.full = make(map[evaluationAggregationKey]*evaluationEntry)
	w.aggregator.degraded = make(map[evaluationDegradedKey]*evaluationEntry)
	w.aggregator.ultraDeg = make(map[evaluationUltraDegradedKey]*evaluationEntry)
	w.aggregator.perFlagFull = make(map[string]int)
	w.aggregator.globalCount = 0

	w.aggregator.mu.Unlock()

	nowMs := time.Now().UnixMilli()
	var events []flagEvaluationEvent

	// Full tier events.
	for key, e := range full {
		ev := flagEvaluationEvent{
			Timestamp:       nowMs,
			Flag:            flagEvalFlag{Key: key.flagKey},
			FirstEvaluation: e.firstEvaluation,
			LastEvaluation:  e.lastEvaluation,
			EvaluationCount: e.count,
			RuntimeDefault:  e.runtimeDefault,
			TargetingKey:    e.targetingKey,
		}
		if key.variant != "" {
			ev.Variant = &flagEvalVariant{Key: key.variant}
		}
		if key.allocationKey != "" {
			ev.Allocation = &flagEvalAllocation{Key: key.allocationKey}
		}
		if e.errorMessage != "" {
			ev.Error = &flagEvalError{Message: e.errorMessage}
		}
		if len(e.contextAttrs) > 0 {
			ev.Context = &flagEvalEventContext{Evaluation: e.contextAttrs}
		}
		events = append(events, ev)
	}

	// Degraded tier events (no targeting_key, no context.evaluation).
	for key, e := range degraded {
		ev := flagEvaluationEvent{
			Timestamp:       nowMs,
			Flag:            flagEvalFlag{Key: key.flagKey},
			FirstEvaluation: e.firstEvaluation,
			LastEvaluation:  e.lastEvaluation,
			EvaluationCount: e.count,
			RuntimeDefault:  e.runtimeDefault,
		}
		if key.variant != "" {
			ev.Variant = &flagEvalVariant{Key: key.variant}
		}
		if key.allocationKey != "" {
			ev.Allocation = &flagEvalAllocation{Key: key.allocationKey}
		}
		events = append(events, ev)
	}

	// Ultra-degraded tier events (only required fields + flag + variant).
	for key, e := range ultraDeg {
		ev := flagEvaluationEvent{
			Timestamp:       nowMs,
			Flag:            flagEvalFlag{Key: key.flagKey},
			FirstEvaluation: e.firstEvaluation,
			LastEvaluation:  e.lastEvaluation,
			EvaluationCount: e.count,
		}
		if key.variant != "" {
			ev.Variant = &flagEvalVariant{Key: key.variant}
		}
		events = append(events, ev)
	}

	if len(events) == 0 {
		return
	}

	payload := flagEvaluationPayload{
		Context:         w.ddContext,
		FlagEvaluations: events,
	}

	if err := w.sendToAgent(payload); err != nil {
		log.Error("openfeature: failed to send flag evaluation events: %v", err.Error())
	} else {
		log.Debug("openfeature: successfully sent %d flag evaluation events", len(events))
	}
}

// record extracts evaluation details and adds them to the aggregation buffer.
// Called from the Finally hook after every evaluation.
func (w *flagEvaluationWriter) record(hookContext of.HookContext, details of.InterfaceEvaluationDetails) {
	// Extract details.
	d := extractEvalDetails(hookContext, details)

	// Flatten and prune the evaluation context.
	attrs := hookContext.EvaluationContext().Attributes()
	var contextAttrs map[string]any
	if len(attrs) > 0 {
		flattened := flattenContext(attrs)
		contextAttrs = pruneContext(flattened)
	}

	nowMs := time.Now().UnixMilli()
	w.aggregator.add(d, contextAttrs, nowMs)
}

// sendToAgent sends the flag evaluation payload to the Datadog Agent via EVP proxy.
// Reuses evpSubdomainHeader / evpSubdomainValue constants from exposure.go (D-04).
func (w *flagEvaluationWriter) sendToAgent(payload flagEvaluationPayload) error {
	var bytesBuffer bytes.Buffer
	encoder := w.jsonConfig.NewEncoder(&bytesBuffer)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("failed to encode flag evaluation payload: %w", err)
	}

	u := *w.agentURL
	u.Path = flagEvaluationEndpoint
	requestURL := u.String()

	req, err := http.NewRequestWithContext(context.Background(), "POST", requestURL, &bytesBuffer)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(evpSubdomainHeader, evpSubdomainValue)

	log.Debug("openfeature: sending flag evaluation events to %s", requestURL)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// add records one evaluation observation into the appropriate aggregation tier.
// Must be called WITHOUT the aggregator lock held (it acquires the lock internally).
// Implements the three-tier cascade: full → degraded → ultra-degraded (CONT-04/CONT-10).
//
// Per-flag attempt counting: perFlagFull[flag] is incremented on every call for a flag
// (whether or not a full-tier bucket is actually created). This ensures that once
// globalCap is full, a flag that accumulates enough attempts (>= perFlagCap) still
// overflows to degraded — keeping the per-flag overflow path alive even after the
// global full-tier cap is exhausted.
func (a *flagEvaluationAggregator) add(d evalDetails, contextAttrs map[string]any, nowMs int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Build the full key using struct fields (collision-safe — CONT-05).
	fullKey := evaluationAggregationKey{
		flagKey:       d.flagKey,
		variant:       d.variant,
		allocationKey: d.allocationKey,
		reason:        d.reason,
		targetingKey:  d.targetingKey,
		contextHash:   hashContext(contextAttrs),
	}

	// Check if this exact full-tier bucket already exists → fast-path increment.
	if e, ok := a.full[fullKey]; ok {
		e.count++
		if nowMs < e.firstEvaluation {
			e.firstEvaluation = nowMs
		}
		if nowMs > e.lastEvaluation {
			e.lastEvaluation = nowMs
		}
		return
	}

	// Check per-flag cap.
	if a.perFlagFull[d.flagKey] >= a.perFlagCap {
		// perFlagCap exceeded — route to degraded tier.
		a.addToDegraded(d, nowMs)
		return
	}

	// Per-flag cap not yet reached. Increment the attempt count for this flag
	// regardless of whether we can actually create a full-tier bucket. This ensures
	// the degraded overflow path activates correctly even when globalCap is full.
	a.perFlagFull[d.flagKey]++

	// Check globalCap before creating a new full-tier bucket.
	if a.globalCount >= a.globalCap {
		// Global cap full — attempt counted above; no new bucket created.
		// The next attempt for this flag may eventually overflow perFlagCap → degraded.
		return
	}

	// New full-tier entry.
	a.full[fullKey] = &evaluationEntry{
		count:           1,
		firstEvaluation: nowMs,
		lastEvaluation:  nowMs,
		runtimeDefault:  d.runtimeDefault,
		targetingKey:    d.targetingKey,
		contextAttrs:    contextAttrs,
		errorMessage:    d.errorMessage,
	}
	a.globalCount++
}

// addToDegraded adds an entry to the degraded map (drops targeting_key + context).
// Called with the aggregator lock held.
// The degraded map is capped by degradedCap; overflow routes to ultra-degraded.
func (a *flagEvaluationAggregator) addToDegraded(d evalDetails, nowMs int64) {
	degKey := evaluationDegradedKey{
		flagKey:       d.flagKey,
		variant:       d.variant,
		allocationKey: d.allocationKey,
		reason:        d.reason,
	}

	if e, ok := a.degraded[degKey]; ok {
		e.count++
		if nowMs < e.firstEvaluation {
			e.firstEvaluation = nowMs
		}
		if nowMs > e.lastEvaluation {
			e.lastEvaluation = nowMs
		}
		return
	}

	// New degraded bucket — check degradedCap.
	if len(a.degraded) >= a.degradedCap {
		// degradedCap exceeded — fall through to ultra-degraded.
		a.addToUltraDegraded(d, nowMs)
		return
	}

	a.degraded[degKey] = &evaluationEntry{
		count:           1,
		firstEvaluation: nowMs,
		lastEvaluation:  nowMs,
		runtimeDefault:  d.runtimeDefault,
	}
}

// addToUltraDegraded adds an entry to the ultra-degraded map (only flag key + variant).
// Called with the aggregator lock held.
// Ultra-degraded has no explicit cap — it is naturally bounded by the flag×variant
// enumeration (CONT-10). The globalCap enforcement applies only to the full tier.
func (a *flagEvaluationAggregator) addToUltraDegraded(d evalDetails, nowMs int64) {
	ultraKey := evaluationUltraDegradedKey{
		flagKey: d.flagKey,
		variant: d.variant,
	}

	if e, ok := a.ultraDeg[ultraKey]; ok {
		e.count++
		if nowMs < e.firstEvaluation {
			e.firstEvaluation = nowMs
		}
		if nowMs > e.lastEvaluation {
			e.lastEvaluation = nowMs
		}
		return
	}

	// New ultra-degraded bucket — always created (naturally bounded by flag×variant).
	a.ultraDeg[ultraKey] = &evaluationEntry{
		count:           1,
		firstEvaluation: nowMs,
		lastEvaluation:  nowMs,
	}
}

// hashContext computes a FNV-1a hash of the pruned context map for use as a
// discriminator inside evaluationAggregationKey. The struct key itself is collision-safe
// across all enumerable dimensions; the hash supplements context identity only.
func hashContext(attrs map[string]any) uint64 {
	if len(attrs) == 0 {
		return 0
	}
	// Hash over a deterministic key ordering. Go map iteration is randomized,
	// and FNV-1a is order-sensitive, so ranging the map directly would produce a
	// different hash for identical contexts and fragment aggregation buckets (CR-01).
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "%s=%v\n", k, attrs[k])
	}
	return h.Sum64()
}

// pruneContext applies 256-field / 256-char limits before buffering (D-07).
// Mirrors worker.ts MAX_EVALUATION_CONTEXT_FIELDS / MAX_FIELD_LENGTH exactly.
// Must be called AFTER flattenContext() (from flatten.go) to expand nested objects first.
func pruneContext(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any, min(len(raw), maxContextFields))
	count := 0
	for k, v := range raw {
		if count >= maxContextFields {
			break
		}
		if s, ok := v.(string); ok && len(s) > maxFieldLength {
			// Skip oversized string values (matches worker.ts pruneFields behavior).
			continue
		}
		out[k] = v
		count++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractEvalDetails extracts EVP-relevant fields from hook context and evaluation details.
// This helper is used only by flagEvaluationHook — it does NOT replace the extraction in
// flageval_metrics.go (that file is untouched per D-06/PRES-01).
func extractEvalDetails(hookContext of.HookContext, details of.InterfaceEvaluationDetails) evalDetails {
	allocationKey, _ := details.FlagMetadata[metadataAllocationKey].(string)
	reason := strings.ToLower(string(details.Reason))
	if reason == "" {
		reason = "unknown"
	}
	var errMsg string
	if details.ErrorCode != "" {
		errMsg = string(details.ErrorCode)
	}
	return evalDetails{
		flagKey:        hookContext.FlagKey(),
		variant:        details.Variant,
		reason:         reason,
		allocationKey:  allocationKey,
		targetingKey:   hookContext.EvaluationContext().TargetingKey(),
		errorMessage:   errMsg,
		runtimeDefault: isRuntimeDefault(details),
	}
}
