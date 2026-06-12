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
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// Dedicated 10 s timer; separate from exposureWriter's 1 s interval.
	defaultFlagEvalFlushInterval = 10 * time.Second

	// flagEvaluationEndpoint is the EVP proxy endpoint for flag evaluation events.
	flagEvaluationEndpoint = "/evp_proxy/v2/api/v2/flagevaluations"

	// Context pruning limits — mirror worker.ts MAX_EVALUATION_CONTEXT_FIELDS / MAX_FIELD_LENGTH.
	maxContextFields = 256
	maxFieldLength   = 256

	// Aggregation caps.
	//
	// SPIKE cq7 (2-tier resize): the cascade is now full → degraded → drop(counted). With no
	// ultra-degraded backstop, degradedCap must hold the FULL legitimate degraded cardinality at
	// the team's >=2,500-flag scale target, or legitimate counts would be dropped. The measured
	// legitimate degraded cardinality at 2,500 flags (Σ variants×allocations×reasons over a
	// realistic flag mix) is 21,662 distinct buckets (see flagevaluation_scale_test.go); so
	// degradedCap is raised to 32,768 (~1.5x headroom). globalCap (full-tier) is raised to
	// 131,072 so a realistic 2,500-flag × multi-context workload keeps full-fidelity buckets
	// before degrading rather than dropping them — overflow from full still cascades to degraded,
	// which is now sized to hold it.
	defaultEvalGlobalCap   = 131_072 // bounds full-tier buckets only; degraded is bounded separately
	defaultEvalPerFlagCap  = 10_000  // bounds full-fidelity buckets per flag
	defaultEvalDegradedCap = 32_768  // bounds degraded map; overflow is dropped(counted), no ultra tier

	// defaultEvalEventBufferSize bounds the async hand-off queue between the (hot-path)
	// Finally hook and the background aggregation worker. On overflow the hook drops the
	// event and increments a counter rather than blocking the evaluation.
	defaultEvalEventBufferSize = 4096
)

// evaluationAggregationKey identifies one full-tier aggregation bucket. Every field is an
// EXACT, comparable string compared byte-for-byte by Go map equality, so distinct keys can
// NEVER alias into the same bucket:
//
//   - The enumerable dimensions (flagKey, variant, allocationKey, reason, targetingKey) are
//     the raw evaluation strings.
//   - contextKey is the EXACT canonical type-tagged, length-delimited encoding of the pruned
//     context (see canonicalContextKey). Because it is the full encoding — not a lossy 64-bit
//     digest — two distinct contexts ALWAYS produce distinct contextKey strings (oleksii #2):
//     int 1 vs string "1" differ by type tag, and '='/'\n'-bearing values cannot fake a field
//     boundary thanks to the length prefixes. There is no hash, so there is no hash collision,
//     so a count can never be misattributed to the wrong context. Go's map hashes and compares
//     the full struct key (including contextKey) natively.
//
// The contextKey string is stored once per full-tier bucket; its memory footprint is therefore
// bounded by the number of full-tier buckets (globalCap) and the pruned context size
// (256 fields × 256 chars), and measured in BenchmarkFlagEvaluationOTelPlusEVPParallel.
type evaluationAggregationKey struct {
	flagKey       string
	variant       string
	allocationKey string
	reason        string
	targetingKey  string
	contextKey    string // exact canonical encoding of the pruned context; comparable, not a digest
}

// evaluationDegradedKey is the key for the degraded aggregation map — the terminal aggregation
// tier in the 2-tier design (full → degraded → drop). Drops targeting key, context, and
// targeting rule key relative to the full key. This is EXACTLY the OTel feature_flag.evaluations
// cardinality. When a NEW degraded bucket would exceed degradedCap, the count is dropped and
// counted (aggregator.dropped) rather than cascading to a further-degraded tier.
type evaluationDegradedKey struct {
	flagKey       string
	variant       string
	allocationKey string
	reason        string
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

// observe records one more evaluation against an existing bucket: it bumps the count and
// widens the [firstEvaluation, lastEvaluation] window to include nowMs. Every existing-bucket
// path across the three tiers funnels through here so the count++/min/max logic lives once.
func (e *evaluationEntry) observe(nowMs int64) {
	e.count++
	if nowMs < e.firstEvaluation {
		e.firstEvaluation = nowMs
	}
	if nowMs > e.lastEvaluation {
		e.lastEvaluation = nowMs
	}
}

// newEvaluationEntry returns a fresh bucket for nowMs with count 1 and first==last==nowMs.
// Callers set any tier-specific fields (runtimeDefault, targetingKey, contextAttrs,
// errorMessage) on the returned entry.
func newEvaluationEntry(nowMs int64) *evaluationEntry {
	return &evaluationEntry{
		count:           1,
		firstEvaluation: nowMs,
		lastEvaluation:  nowMs,
	}
}

// flagEvaluationAggregator holds the two-tier aggregation maps (full → degraded → drop).
type flagEvaluationAggregator struct {
	mu          sync.Mutex
	full        map[evaluationAggregationKey]*evaluationEntry
	degraded    map[evaluationDegradedKey]*evaluationEntry
	perFlagFull map[string]int // flagKey → count of full-fidelity entries for this flag
	globalCount int
	globalCap   int
	perFlagCap  int
	degradedCap int
	// dropped counts evaluations whose count was lost because a NEW degraded bucket would have
	// exceeded degradedCap (the terminal tier in the 2-tier design). It is the observable signal
	// that legitimate counts are being dropped and that degradedCap should be raised. It is
	// distinct from flagEvaluationWriter.dropped (which counts async-queue backpressure drops).
	droppedDegradedOverflow int64
}

// flagEvaluationEvent matches flagevaluation.json — required fields always present;
// optional fields use omitempty (absent = schema-valid for the degraded tier).
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
	stopped       atomic.Bool // single idempotency gate for stop(); also read lock-free by record()
	jsonConfig    jsoniter.API

	// Asynchronous hand-off: the Finally hook enqueues a cheap snapshot here; a single
	// background worker (started in start()) drains it and performs flatten/prune/hash/
	// aggregate off the evaluation hot path. events is bounded; on overflow the hook drops
	// the event and bumps dropped — best-effort telemetry that never blocks the request.
	events     chan evalEvent
	dropped    atomic.Int64
	workerDone chan struct{}
}

// evalEvent is the minimal snapshot the Finally hook hands to the worker. attrs is the
// owned copy returned by EvaluationContext().Attributes(), so it is safe to read off the
// hot path for scalar values (a nested mutable attribute the caller mutates after the call
// returns is a documented best-effort edge).
type evalEvent struct {
	d     evalDetails
	attrs map[string]any
	nowMs int64
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
// The writer uses the same HTTP transport setup as exposure.go.
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
		workerDone:    make(chan struct{}),
		events:        make(chan evalEvent, defaultEvalEventBufferSize),
		jsonConfig:    jsoniter.Config{}.Froze(),
		ddContext: flagEvalDDContext{
			Service: cmp.Or(env.Get("DD_SERVICE"), globalconfig.ServiceName(), executable),
			Version: env.Get("DD_VERSION"),
			Env:     env.Get("DD_ENV"),
		},
		aggregator: flagEvaluationAggregator{
			full:        make(map[evaluationAggregationKey]*evaluationEntry),
			degraded:    make(map[evaluationDegradedKey]*evaluationEntry),
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
			// Always signal completion so stop() unblocks, even on panic.
			close(w.workerDone)
		}()

		// Single owner of flatten/prune/hash/aggregate/flush. The hot path only enqueues;
		// all that cost lives here, off the evaluation path.
		for {
			select {
			case ev := <-w.events:
				w.aggregate(ev)
			case <-w.ticker.C:
				w.flush()
			case <-w.stopChan:
				w.drainAndFlush()
				return
			}
		}
	}()
}

// stop stops the flush ticker and marks the writer as stopped.
func (w *flagEvaluationWriter) stop() {
	// Single idempotency gate: the atomic Swap is the ONLY guard for both "mark stopped" and
	// the downstream close(stopChan). A second/concurrent stop() sees Swap return true and
	// returns early, so stopChan is closed exactly once (no double-close panic). record() reads
	// this flag lock-free to no-op post-stop enqueues.
	if w.stopped.Swap(true) {
		return
	}

	// Signal the worker to drain the queue and do a final flush.
	close(w.stopChan)
	if w.ticker != nil {
		// Worker was started: wait for its final flush before returning, then stop the
		// ticker. (ticker is set only in start(), so it gates "was the worker started".)
		<-w.workerDone
		w.ticker.Stop()
	}

	log.Debug("openfeature: flag evaluation writer stopped")
}

// flush drains the aggregator, assembles per-tier events, and sends them to the agent.
func (w *flagEvaluationWriter) flush() {
	// Surface best-effort backpressure drops (queue full) as an observable signal.
	if d := w.dropped.Swap(0); d > 0 {
		log.Warn("openfeature: flag evaluation queue full — dropped %d evaluation(s) under backpressure (best-effort telemetry)", d)
	}

	w.aggregator.mu.Lock()

	// Under lock: drain both maps.
	full := w.aggregator.full
	degraded := w.aggregator.degraded

	// Surface degraded-overflow drops (the terminal-tier backstop in the 2-tier design) so an
	// undersized degradedCap is observable rather than a silent loss of legitimate counts.
	degradedOverflow := w.aggregator.droppedDegradedOverflow

	if (len(full) + len(degraded)) == 0 {
		w.aggregator.droppedDegradedOverflow = 0
		w.aggregator.mu.Unlock()
		if degradedOverflow > 0 {
			log.Warn("openfeature: degraded aggregation tier full — dropped %d evaluation(s); raise degradedCap (best-effort telemetry)", degradedOverflow)
		}
		return
	}

	// Reset maps.
	w.aggregator.full = make(map[evaluationAggregationKey]*evaluationEntry)
	w.aggregator.degraded = make(map[evaluationDegradedKey]*evaluationEntry)
	w.aggregator.perFlagFull = make(map[string]int)
	w.aggregator.globalCount = 0
	w.aggregator.droppedDegradedOverflow = 0

	w.aggregator.mu.Unlock()

	if degradedOverflow > 0 {
		log.Warn("openfeature: degraded aggregation tier full — dropped %d evaluation(s); raise degradedCap (best-effort telemetry)", degradedOverflow)
	}

	nowMs := time.Now().UnixMilli()
	var events []flagEvaluationEvent

	// Full tier: required fields + variant + allocation + targeting_key + context + error;
	// runtime_default decorates this tier.
	for key, e := range full {
		ev := baseFlagEvaluationEvent(key.flagKey, e, nowMs)
		ev.RuntimeDefault = e.runtimeDefault
		ev.TargetingKey = e.targetingKey
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

	// Degraded tier: required fields + variant + allocation; no targeting_key, no context.
	// runtime_default decorates this tier.
	for key, e := range degraded {
		ev := baseFlagEvaluationEvent(key.flagKey, e, nowMs)
		ev.RuntimeDefault = e.runtimeDefault
		if key.variant != "" {
			ev.Variant = &flagEvalVariant{Key: key.variant}
		}
		if key.allocationKey != "" {
			ev.Allocation = &flagEvalAllocation{Key: key.allocationKey}
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

// baseFlagEvaluationEvent builds a flagEvaluationEvent with ONLY the five required schema
// fields (timestamp, flag.key, first/last evaluation, evaluation_count). It is tier-agnostic
// and sets no optional field — RuntimeDefault and the rest are decoration applied by each tier
// loop in flush() after the call.
func baseFlagEvaluationEvent(flagKey string, e *evaluationEntry, nowMs int64) flagEvaluationEvent {
	return flagEvaluationEvent{
		Timestamp:       nowMs,
		Flag:            flagEvalFlag{Key: flagKey},
		FirstEvaluation: e.firstEvaluation,
		LastEvaluation:  e.lastEvaluation,
		EvaluationCount: e.count,
	}
}

// record runs on the evaluation hot path (the Finally hook). It does only cheap scalar
// extraction plus the SDK's shallow context copy, then a non-blocking enqueue — no
// flatten/prune/hash/aggregation happens here; the background worker does that. If the
// queue is full the event is dropped and counted (best-effort), never blocking the
// evaluation. Called from the Finally hook after every evaluation.
func (w *flagEvaluationWriter) record(hookContext of.HookContext, details of.InterfaceEvaluationDetails) {
	// Post-stop no-op: after stop() the worker no longer drains w.events, so enqueuing would
	// silently lose the event. Check the atomic gate lock-free (reading under the aggregator
	// lock would add hot-path contention) and count the event as dropped so it stays observable.
	if w.stopped.Load() {
		w.dropped.Add(1)
		return
	}
	ev := evalEvent{
		d:     extractEvalDetails(hookContext, details),
		attrs: hookContext.EvaluationContext().Attributes(), // SDK returns an owned copy
		nowMs: time.Now().UnixMilli(),
	}
	select {
	case w.events <- ev:
	default:
		w.dropped.Add(1)
	}
}

// aggregate performs the deferred flatten/prune/key and updates the aggregator. It runs
// only on the writer's single worker goroutine.
func (w *flagEvaluationWriter) aggregate(ev evalEvent) {
	var contextAttrs map[string]any
	if len(ev.attrs) > 0 {
		contextAttrs = flattenAndPruneContext(ev.attrs)
	}
	w.aggregator.add(ev.d, contextAttrs, ev.nowMs)
}

// flattenAndPruneContext is the worker-path procedure that produces the pruned context map for
// EVP aggregation in a SINGLE traversal of the flattened keyspace (oleksii #4). It merges the
// two former steps — flattenContext (flatten.go) + pruneContext — into one pass with the SAME
// pruned output:
//
//  1. Flatten nested objects into a single-level dot-notation map (reusing flattenRecursive, so
//     the flatten semantics stay identical to the exposure path which still calls
//     flattenContext directly — that caller is unchanged).
//  2. Apply the deterministic prune: sort the flattened keys, then keep the first
//     maxContextFields that are not oversized strings (>maxFieldLength).
//
// Allocation win: when the flattened context already fits the limits (the common case — fewer
// than maxContextFields fields and no oversized string), the flattened map is returned DIRECTLY,
// so the separate pruned-output map that the old flatten→prune pipeline always allocated is
// elided. The pruned map is allocated only when trimming actually changes the result. Output is
// byte-for-byte identical to the previous flattenContext+pruneContext pipeline: same surviving
// keys, same 256/256 limits, same deterministic ordering of the cut.
func flattenAndPruneContext(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}

	flat := make(map[string]any, len(attrs))
	flattenRecursive("", attrs, flat)
	if len(flat) == 0 {
		return nil
	}

	// Determine whether any pruning is actually required: an over-cap field count or any
	// oversized string value. If neither, the flattened map already IS the pruned result —
	// return it directly and skip allocating a second map.
	needsPrune := len(flat) > maxContextFields
	if !needsPrune {
		for _, v := range flat {
			if s, ok := v.(string); ok && len(s) > maxFieldLength {
				needsPrune = true
				break
			}
		}
	}
	if !needsPrune {
		return flat
	}

	// Deterministic prune: sort keys, then keep the first maxContextFields non-oversized values.
	// Sorting BEFORE the oversized-string skip and the field cap makes the kept subset stable
	// across calls (Go map iteration is randomized), so logically-identical contexts always
	// prune to the same subset and the same canonicalContextKey.
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]any, min(len(flat), maxContextFields))
	count := 0
	for _, k := range keys {
		if count >= maxContextFields {
			break
		}
		v := flat[k]
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

// drainAndFlush processes any buffered events and performs a final flush. Called by the
// worker when stopping so a final batch is not lost on shutdown.
func (w *flagEvaluationWriter) drainAndFlush() {
	for {
		select {
		case ev := <-w.events:
			w.aggregate(ev)
		default:
			w.flush()
			return
		}
	}
}

// sendToAgent sends the flag evaluation payload to the Datadog Agent via EVP proxy.
// Reuses evpSubdomainHeader / evpSubdomainValue constants from exposure.go.
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
// Implements the two-tier cascade: full → degraded → drop(counted).
//
// Per-flag attempt counting: perFlagFull[flag] is incremented on every call for a flag
// (whether or not a full-tier bucket is actually created). This ensures that once
// globalCap is full, a flag that accumulates enough attempts (>= perFlagCap) still
// overflows to degraded — keeping the per-flag overflow path alive even after the
// global full-tier cap is exhausted.
func (a *flagEvaluationAggregator) add(d evalDetails, contextAttrs map[string]any, nowMs int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Build the full key: exact, comparable strings for every dimension including the canonical
	// context encoding. No hash, so distinct contexts get distinct buckets (oleksii #2).
	fullKey := evaluationAggregationKey{
		flagKey:       d.flagKey,
		variant:       d.variant,
		allocationKey: d.allocationKey,
		reason:        d.reason,
		targetingKey:  d.targetingKey,
		contextKey:    canonicalContextKey(contextAttrs),
	}

	// Fast path: this exact full-tier bucket already exists → increment its count. Because
	// contextKey is the full canonical encoding (not a digest), this fast path is hit only by a
	// genuinely identical pruned context — never by an aliasing collision.
	if e, ok := a.full[fullKey]; ok {
		e.observe(nowMs)
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
		// Global full-tier cap full — count must not be lost. Route into the degraded tier
		// (which drops targeting_key + context), sized to hold the legitimate degraded
		// cardinality at the >=2,500-flag scale target. The per-flag attempt counter was
		// already incremented above; once it reaches perFlagCap this flag routes through
		// addToDegraded directly as well.
		a.addToDegraded(d, nowMs)
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
// Called with the aggregator lock held. Degraded is the TERMINAL aggregation tier in the
// 2-tier design: when a NEW degraded bucket would exceed degradedCap, the evaluation's count is
// DROPPED and counted (droppedDegradedOverflow) rather than cascading to a further-degraded
// tier. degradedCap is sized (defaultEvalDegradedCap) to hold the legitimate degraded
// cardinality at the >=2,500-flag scale target, so this drop only fires under cardinality far
// beyond that target (e.g. an unbounded dynamic/abusive flag key — reviewer concern #8) and the
// dropped counter makes such overflow observable.
func (a *flagEvaluationAggregator) addToDegraded(d evalDetails, nowMs int64) {
	degKey := evaluationDegradedKey{
		flagKey:       d.flagKey,
		variant:       d.variant,
		allocationKey: d.allocationKey,
		reason:        d.reason,
	}

	if e, ok := a.degraded[degKey]; ok {
		e.observe(nowMs)
		return
	}

	// New degraded bucket — check degradedCap.
	if len(a.degraded) >= a.degradedCap {
		// degradedCap exceeded — terminal tier full. Drop the count but keep it observable so an
		// undersized cap surfaces in the flush warning instead of silently losing legitimate data.
		a.droppedDegradedOverflow++
		return
	}

	e := newEvaluationEntry(nowMs)
	e.runtimeDefault = d.runtimeDefault
	a.degraded[degKey] = e
}

// context value type discriminators for the canonical key encoding. Each distinct Go type
// gets a distinct tag byte so that, e.g., int 1 and string "1" cannot render identically.
const (
	ctxTagString  byte = 's'
	ctxTagBool    byte = 'b'
	ctxTagInt     byte = 'i'
	ctxTagInt64   byte = 'l'
	ctxTagInt32   byte = 'j'
	ctxTagFloat64 byte = 'f'
	ctxTagFloat32 byte = 'g'
	ctxTagOther   byte = 'o'
)

// canonicalContextKey builds the EXACT, comparable string key for the pruned context map,
// used as the contextKey field of evaluationAggregationKey.
//
// The encoding is CANONICAL — each field is a length-delimited key followed by a type-tag byte
// and a length-delimited value — so distinct contexts cannot ALIAS by construction (int 1 vs
// string "1" differ by tag; '=' / '\n' cannot fake a field boundary). Unlike the prior FNV-1a
// digest, the full encoding is emitted AS THE KEY, so Go's map compares it byte-for-byte: there
// is no hash and therefore no hash collision, so distinct contexts ALWAYS land in distinct
// full-tier buckets (oleksii #2). The returned string is stored once per full-tier bucket.
func canonicalContextKey(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	// Encode over a deterministic key ordering. Go map iteration is randomized, and the
	// concatenated encoding is order-sensitive, so ranging the map directly would produce a
	// different key for identical contexts and fragment aggregation buckets.
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Build the encoding into a single buffer, then convert once to a string for the key. The
	// per-field append uses the same canonical, allocation-light path as before.
	var buf []byte
	for _, k := range keys {
		buf = appendLengthDelimited(buf, []byte(k)) // length-delimited key
		buf = appendContextValue(buf, attrs[k])     // tag + length-delimited value
	}
	return string(buf)
}

// appendLengthDelimited writes a fixed-width 8-byte big-endian length prefix followed by the
// raw bytes, so the boundary between fields is unambiguous regardless of the byte content.
func appendLengthDelimited(buf, b []byte) []byte {
	var lenBuf [8]byte
	n := uint64(len(b))
	for i := range 8 {
		lenBuf[7-i] = byte(n)
		n >>= 8
	}
	buf = append(buf, lenBuf[:]...)
	return append(buf, b...)
}

// appendContextValue appends a CANONICAL, length-delimited rendering of v to buf: a type-tag
// byte (distinct per Go type) followed by a length-delimited rendered value. This avoids
// allocation for the common scalar types; rare/complex types fall back to fmt. The encoding
// only needs to be deterministic within a run and collision-free across distinct values.
func appendContextValue(buf []byte, v any) []byte {
	var scratch [32]byte
	tmp := scratch[:0]
	var tag byte
	switch x := v.(type) {
	case string:
		tag = ctxTagString
		tmp = append(tmp, x...)
	case bool:
		tag = ctxTagBool
		tmp = strconv.AppendBool(tmp, x)
	case int:
		tag = ctxTagInt
		tmp = strconv.AppendInt(tmp, int64(x), 10)
	case int64:
		tag = ctxTagInt64
		tmp = strconv.AppendInt(tmp, x, 10)
	case int32:
		tag = ctxTagInt32
		tmp = strconv.AppendInt(tmp, int64(x), 10)
	case float64:
		tag = ctxTagFloat64
		tmp = strconv.AppendFloat(tmp, x, 'g', -1, 64)
	case float32:
		tag = ctxTagFloat32
		tmp = strconv.AppendFloat(tmp, float64(x), 'g', -1, 32)
	default:
		tag = ctxTagOther
		tmp = append(tmp, fmt.Sprintf("%v", x)...)
	}
	buf = append(buf, tag)
	return appendLengthDelimited(buf, tmp)
}

// pruneContext applies 256-field / 256-char limits before buffering.
// Mirrors worker.ts MAX_EVALUATION_CONTEXT_FIELDS / MAX_FIELD_LENGTH exactly.
// Must be called AFTER flattenContext() (from flatten.go) to expand nested objects first.
//
// The kept subset is DETERMINISTIC: keys are sorted BEFORE the oversized-string skip and the
// 256-field cap are applied, so two logically-identical contexts always prune to the exact
// same subset (and therefore the same canonicalContextKey). Ranging the map directly (Go map
// iteration is randomized) would cut a different 256-field subset each call and fragment
// otherwise-identical contexts into separate aggregation buckets.
func pruneContext(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]any, min(len(raw), maxContextFields))
	count := 0
	for _, k := range keys {
		if count >= maxContextFields {
			break
		}
		v := raw[k]
		if s, ok := v.(string); ok && len(s) > maxFieldLength {
			// Skip oversized string values (matches worker.ts pruneFields behavior).
			// Applied against the deterministic ordering so the kept subset is stable.
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
// flageval_metrics.go (that file is left untouched to preserve the OTel path).
func extractEvalDetails(hookContext of.HookContext, details of.InterfaceEvaluationDetails) evalDetails {
	allocationKey, _ := details.FlagMetadata[metadataAllocationKey].(string)
	reason := strings.ToLower(string(details.Reason))
	if reason == "" {
		reason = "unknown"
	}
	// Prefer OpenFeature's human-readable ErrorMessage; fall back to the ErrorCode string only
	// when ErrorMessage is empty (some providers populate just the code).
	errMsg := details.ErrorMessage
	if errMsg == "" && details.ErrorCode != "" {
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
