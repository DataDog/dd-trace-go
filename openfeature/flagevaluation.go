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
	defaultEvalGlobalCap   = 65_536 // bounds full-tier buckets only; degraded/ultra are bounded separately
	defaultEvalPerFlagCap  = 10_000 // bounds full-fidelity buckets per flag
	defaultEvalDegradedCap = 10_000 // bounds degraded map

	// defaultEvalEventBufferSize bounds the async hand-off queue between the (hot-path)
	// Finally hook and the background aggregation worker. On overflow the hook drops the
	// event and increments a counter rather than blocking the evaluation.
	defaultEvalEventBufferSize = 4096
)

// evaluationAggregationKey identifies one full-tier aggregation bucket. Its identity is
// split into two parts with very different collision properties:
//
//  1. Enumerable dimensions — flagKey, variant, allocationKey, reason, targetingKey — are
//     stored as EXACT string fields and compared byte-for-byte by Go map equality. Two
//     structurally distinct values can NEVER alias on these dimensions. In particular, a
//     count can never be attributed to the wrong flag/variant/reason/allocation.
//
//  2. contextHash is a 64-bit FNV-1a digest of the pruned evaluation context (see
//     hashContext, hashed over sorted keys for determinism). The context is map[string]any
//     and therefore not comparable, so it cannot be a struct field — the digest stands in
//     for it. The digest is deterministic and low-collision but NOT collision-proof.
//
// What a contextHash collision actually does — and why it is acceptable:
//
//	A collision requires two evaluations that are identical on ALL FIVE exact dimensions
//	AND whose *different* contexts happen to hash to the same uint64. When that occurs, the
//	second evaluation matches the existing bucket on add()'s fast path and increments its
//	count (the `if e, ok := a.full[fullKey]; ok` branch below). Consequences:
//
//	  - COUNT-PRESERVING: the evaluation count is merged into the existing bucket, never
//	    dropped. The invariant Σ(counts across all tiers) == number of add() calls still
//	    holds. This is exactly what TestSaturationCountPreservation guards.
//	  - NO MISATTRIBUTION: the count still belongs to the correct flag/variant/reason/
//	    allocation — those dimensions are exact, not hashed.
//	  - SOLE CASUALTY is context-attribute fidelity: the bucket retains the FIRST context's
//	    attrs, so the colliding evaluation's distinct attrs are not separately reported.
//	    Context is a best-effort dimension that the degraded/ultra tiers drop entirely by
//	    design, so a collision is strictly less lossy than ordinary degradation.
//
//	Probability is bounded by the cap: the full tier holds at most globalCap (65536)
//	buckets, so there are <= ~65k distinct digests per flush window. Birthday bound
//	~= 65536^2 / 2^65 ~= 1.2e-10 per window. This is internal telemetry over the customer's
//	own context — not a trust boundary — so a keyed/crypto hash (e.g. SipHash) is
//	unwarranted, and a wider (128-bit) digest would buy only context-label fidelity at that
//	1e-10 tail. Deliberately not done.
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
	w.aggregator.mu.Lock()
	if w.stopped {
		w.aggregator.mu.Unlock()
		return
	}
	w.stopped = true
	w.aggregator.mu.Unlock()

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

	// Under lock: drain all three maps.
	full := w.aggregator.full
	degraded := w.aggregator.degraded
	ultraDeg := w.aggregator.ultraDeg

	if (len(full) + len(degraded) + len(ultraDeg)) == 0 {
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

// record runs on the evaluation hot path (the Finally hook). It does only cheap scalar
// extraction plus the SDK's shallow context copy, then a non-blocking enqueue — no
// flatten/prune/hash/aggregation happens here; the background worker does that. If the
// queue is full the event is dropped and counted (best-effort), never blocking the
// evaluation. Called from the Finally hook after every evaluation.
func (w *flagEvaluationWriter) record(hookContext of.HookContext, details of.InterfaceEvaluationDetails) {
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

// aggregate performs the deferred flatten/prune/hash and updates the aggregator. It runs
// only on the writer's single worker goroutine.
func (w *flagEvaluationWriter) aggregate(ev evalEvent) {
	var contextAttrs map[string]any
	if len(ev.attrs) > 0 {
		flattened := flattenContext(ev.attrs)
		contextAttrs = pruneContext(flattened)
	}
	w.aggregator.add(ev.d, contextAttrs, ev.nowMs)
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
// Implements the three-tier cascade: full → degraded → ultra-degraded.
//
// Per-flag attempt counting: perFlagFull[flag] is incremented on every call for a flag
// (whether or not a full-tier bucket is actually created). This ensures that once
// globalCap is full, a flag that accumulates enough attempts (>= perFlagCap) still
// overflows to degraded — keeping the per-flag overflow path alive even after the
// global full-tier cap is exhausted.
func (a *flagEvaluationAggregator) add(d evalDetails, contextAttrs map[string]any, nowMs int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Build the full key: exact struct fields for enumerable dims + 64-bit FNV context digest.
	fullKey := evaluationAggregationKey{
		flagKey:       d.flagKey,
		variant:       d.variant,
		allocationKey: d.allocationKey,
		reason:        d.reason,
		targetingKey:  d.targetingKey,
		contextHash:   hashContext(contextAttrs),
	}

	// Fast path: this exact full-tier bucket already exists → increment its count.
	//
	// This branch is ALSO where a contextHash collision lands (see the
	// evaluationAggregationKey doc): if two evaluations share all five exact dimensions and
	// their differing contexts hash equal, the second matches here and merges into the
	// first's bucket. The count is preserved — never dropped, never misattributed; only the
	// second context's distinct attrs are not separately recorded. This is the
	// count-preserving guarantee TestSaturationCountPreservation asserts.
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
		// Global cap full — count must not be lost.
		// Route into ultra-degraded (flag key + counts only) so the count signal is preserved.
		// The per-flag attempt counter was already incremented above; once it reaches
		// perFlagCap this flag will route through addToDegraded instead.
		a.addToUltraDegraded(d, nowMs)
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
// enumeration. The globalCap enforcement applies only to the full tier.
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

// hashContext computes a deterministic 64-bit FNV-1a hash of the pruned context map for
// use as a discriminator inside evaluationAggregationKey. The enumerable struct fields
// (flagKey, variant, allocationKey, reason, targetingKey) are exact string comparisons and
// cannot collide; contextHash is a probabilistic supplement for context identity only —
// it is low-collision but NOT collision-proof.
//
// A digest collision is count-preserving (it merges into an existing bucket via add()'s
// fast path, costing only context-attribute fidelity, never a count) — see the
// evaluationAggregationKey doc for the full collision analysis.
func hashContext(attrs map[string]any) uint64 {
	if len(attrs) == 0 {
		return 0
	}
	// Hash over a deterministic key ordering. Go map iteration is randomized,
	// and FNV-1a is order-sensitive, so ranging the map directly would produce a
	// different hash for identical contexts and fragment aggregation buckets.
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	// Reuse one scratch buffer across fields and write the bytes directly. FNV's Write
	// neither retains the slice nor allocates, so hashing an N-field context costs ~1
	// allocation instead of the per-field reflection allocation of fmt.Fprintf — the
	// dominant per-evaluation cost of this hook for large contexts.
	var buf []byte
	for _, k := range keys {
		buf = buf[:0]
		buf = append(buf, k...)
		buf = append(buf, '=')
		buf = appendContextValue(buf, attrs[k])
		buf = append(buf, '\n')
		_, _ = h.Write(buf)
	}
	return h.Sum64()
}

// appendContextValue appends a deterministic string form of v to buf, avoiding allocation
// for the common scalar types; rare/complex types fall back to fmt. The hash is only an
// in-memory, per-flush-window discriminator, so the exact formatting is irrelevant as long
// as it is deterministic within a run.
func appendContextValue(buf []byte, v any) []byte {
	switch x := v.(type) {
	case string:
		return append(buf, x...)
	case bool:
		return strconv.AppendBool(buf, x)
	case int:
		return strconv.AppendInt(buf, int64(x), 10)
	case int64:
		return strconv.AppendInt(buf, x, 10)
	case int32:
		return strconv.AppendInt(buf, int64(x), 10)
	case float64:
		return strconv.AppendFloat(buf, x, 'g', -1, 64)
	case float32:
		return strconv.AppendFloat(buf, float64(x), 'g', -1, 32)
	default:
		return append(buf, fmt.Sprintf("%v", x)...)
	}
}

// pruneContext applies 256-field / 256-char limits before buffering.
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
// flageval_metrics.go (that file is left untouched to preserve the OTel path).
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
