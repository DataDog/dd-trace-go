// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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

	// flagEvalLoggingEndpoint is the EVP proxy endpoint for flag evaluation events.
	flagEvalLoggingEndpoint = "/evp_proxy/v2/api/v2/flagevaluation"

	// Context pruning limits — mirror worker.ts MAX_EVALUATION_CONTEXT_FIELDS / MAX_FIELD_LENGTH
	// and align with the cross-SDK RFC (see Java DDEvaluator.copyPrunedContext caps).
	maxContextFields = 256
	maxFieldLength   = 256
	// maxKeyLength bounds the length of a flattened key. Deep nested keys (e.g. many-level
	// dot-notation paths) can outgrow the value cap on the key side; this cap keeps them off
	// the wire and out of the aggregation key.
	maxKeyLength = 256

	// MAX_LIST_ELEMENTS / MAX_STRUCTURE_PROPERTIES / MAX_SNAPSHOT_DEPTH from the cross-SDK RFC
	// are NOT applied here yet. Go's flatten path (flattenRecursive in flatten.go) is shared
	// with the exposure track, and bounding depth/list/struct traversal there would silently
	// drop attributes that the exposure track today emits. Tracked as a follow-up; the field
	// count + key length + value length caps carry the memory-safety load for now.

	// Context truncation reason labels — surface via metrics so operators can tell which cap
	// pressured the context (matches Java's countContextTruncated reason label).
	truncReasonMaxContextFields = "max_context_fields"
	truncReasonMaxFieldLength   = "max_field_length"
	truncReasonMaxKeyLength     = "max_key_length"

	// Aggregation cap sizing inputs.
	evalScaleTargetFlags               = 2_500
	evalScaleFullBucketsPerFlag        = 50
	evalScaleUsersPerFlag              = 1_000
	evalScalePerFlagHeadroomMultiplier = 10
	evalScaleDegradedBucketsPerFlag    = 10
	evalScaleFullBucketTarget          = evalScaleTargetFlags * evalScaleFullBucketsPerFlag
	evalScalePerFlagBucketTarget       = evalScalePerFlagHeadroomMultiplier * evalScaleUsersPerFlag
	evalScaleDegradedBucketTarget      = evalScaleTargetFlags * evalScaleDegradedBucketsPerFlag

	// Aggregation caps.
	//
	// The aggregation path is full → degraded → drop(counted). degradedCap must hold the
	// legitimate degraded cardinality at the target scale, or legitimate counts would be
	// dropped. Degraded cardinality is based only on schema-visible dimensions retained by
	// the degraded payload; OpenFeature reason is not an EVP field and is not part of bucket
	// identity.
	//
	// Sizing arithmetic:
	//   - globalCap 131,072 (2^17) > evalScaleFullBucketTarget = 125,000
	//   - perFlagCap 10,000 = evalScalePerFlagBucketTarget
	//   - degradedCap 32,768 (2^15) > evalScaleDegradedBucketTarget = 25,000
	// These are starting caps, not schema limits; overflow is counted and dropped.
	defaultEvalGlobalCap   = 131_072 // bounds full-tier buckets only; degraded is bounded separately
	defaultEvalPerFlagCap  = evalScalePerFlagBucketTarget
	defaultEvalDegradedCap = 32_768 // bounds degraded buckets; overflow is counted and dropped

	// defaultEvalEventBufferSize bounds the async hand-off queue between the (hot-path)
	// Finally hook and the background aggregation worker. On overflow the hook drops the
	// event and increments a counter rather than blocking the evaluation.
	defaultEvalEventBufferSize = 4096
)

// evaluationAggregationKey identifies one full-tier aggregation bucket. Every field is an
// EXACT, comparable string compared byte-for-byte by Go map equality, so distinct keys can
// NEVER alias into the same bucket:
//
//   - The enumerable dimensions are schema-visible fields only. OpenFeature reason is not
//     accepted by the flageval-worker schema and must never be hidden cardinality.
//   - contextKey is the EXACT canonical type-tagged, length-delimited encoding of the pruned
//     context (see canonicalContextKey). Because it is the full encoding — not a lossy 64-bit
//     digest — two distinct contexts ALWAYS produce distinct contextKey strings:
//     int 1 vs string "1" differ by type tag, and '='/'\n'-bearing values cannot fake a field
//     boundary thanks to the length prefixes. There is no hash, so there is no hash collision,
//     so a count can never be misattributed to the wrong context. Go's map hashes and compares
//     the full struct key (including contextKey) natively.
//
// The contextKey string is stored once per full-tier bucket; its memory footprint is therefore
// bounded by the number of full-tier buckets (globalCap) and the pruned context size
// (256 fields × 256 chars), and measured in BenchmarkFlagEvaluationOTelPlusEVPParallel.
type evaluationAggregationKey struct {
	flagKey        string
	variant        string
	allocationKey  string
	runtimeDefault bool
	errorMessage   string
	targetingKey   string
	contextKey     string // exact canonical encoding of the pruned context; comparable, not a digest
	// observeFullEvaluationData keeps consent-on and consent-off traffic in separate buckets.
	// When false, contextKey is always empty (enforced in add) — the key must only carry
	// dimensions that survive serialization, or one context field would inflate cardinality
	// and burn perFlagCap on the privacy-protected path.
	observeFullEvaluationData bool
}

// evaluationDegradedKey is the key for the degraded aggregation map in the
// full → degraded → drop path. It drops targeting key, context, and other full-only fields,
// keeping only schema-visible fields emitted by the degraded payload. When a NEW degraded
// bucket would exceed degradedCap, the count is dropped and counted.
//
// Consent is intentionally not a dimension: the degraded payload emits neither targeting_key
// nor context, so consent-differing rows would be byte-identical on the wire.
type evaluationDegradedKey struct {
	flagKey        string
	variant        string
	allocationKey  string
	runtimeDefault bool
	errorMessage   string
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
	// observeFullEvaluationData drives serialization: raw targeting_key + context, or hashed
	// key with no context. Duplicates the key field on purpose — add AND-folds it, so any
	// drift from the key still forces the whole bucket to the privacy-protected path.
	observeFullEvaluationData bool
}

// observe records one more evaluation against an existing bucket: it bumps the count and
// widens the [firstEvaluation, lastEvaluation] window to include evaluationTimeMs. Every existing-bucket
// path across the aggregation tiers funnels through here so the count++/min/max logic lives once.
// evaluationTimeMs is captured before enqueue; concurrent evaluations can reach the worker out
// of timestamp order, so an existing bucket can legitimately observe an older timestamp.
func (e *evaluationEntry) observe(evaluationTimeMs int64) {
	e.count++
	if evaluationTimeMs < e.firstEvaluation {
		e.firstEvaluation = evaluationTimeMs
	}
	if evaluationTimeMs > e.lastEvaluation {
		e.lastEvaluation = evaluationTimeMs
	}
}

// newEvaluationEntry returns a fresh bucket for evaluationTimeMs with count 1 and
// first==last==evaluationTimeMs.
// Callers set any tier-specific fields (runtimeDefault, targetingKey, contextAttrs,
// errorMessage) on the returned entry.
func newEvaluationEntry(evaluationTimeMs int64) *evaluationEntry {
	return &evaluationEntry{
		count:           1,
		firstEvaluation: evaluationTimeMs,
		lastEvaluation:  evaluationTimeMs,
	}
}

// flagEvalLoggingAggregator holds the two-tier aggregation maps (full → degraded → drop).
type flagEvalLoggingAggregator struct {
	mu          sync.Mutex
	full        map[evaluationAggregationKey]*evaluationEntry
	degraded    map[evaluationDegradedKey]*evaluationEntry
	perFlagFull map[string]int // flagKey → count of full-fidelity entries for this flag
	globalCount int
	globalCap   int
	perFlagCap  int
	degradedCap int
	// dropped counts evaluations whose count was lost because a NEW degraded bucket would have
	// exceeded degradedCap. It is the observable signal that legitimate counts are being
	// dropped and that degradedCap should be raised. It is distinct from flagEvalLoggingWriter.dropped
	// (which counts async-queue backpressure drops).
	droppedDegradedOverflow int64
}

// flagEvalLoggingEvent matches flagevaluation.json — required fields always present;
// optional fields use omitempty (absent = schema-valid for the degraded tier).
type flagEvalLoggingEvent struct {
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

// flagEvalLoggingPayload is the SDK's EVP flagevaluation batch envelope.
// Keep JSON field names aligned with the worker contract; do not vendor the
// worker schema here, because dd-source owns that contract.
type flagEvalLoggingPayload struct {
	Context         flagEvalDDContext      `json:"context"`
	FlagEvaluations []flagEvalLoggingEvent `json:"flagEvaluations"`
}

// flagEvalDDContext carries service/env/version for the batch-level context.
type flagEvalDDContext struct {
	Service string `json:"service"`
	Env     string `json:"env,omitempty"`
	Version string `json:"version,omitempty"`
}

// flagEvalLoggingWriter manages aggregation and periodic flushing of EVP flag evaluation events.
type flagEvalLoggingWriter struct {
	aggregator    flagEvalLoggingAggregator
	flushInterval time.Duration
	evp           *evpClient
	ddContext     flagEvalDDContext // service/env/version — same source as exposureContext
	ticker        *time.Ticker
	stopChan      chan struct{}
	stopped       atomic.Bool // single idempotency gate for stop(); also read lock-free by record()

	// Asynchronous hand-off: the Finally hook enqueues a bounded snapshot here; a single
	// background worker (started in start()) drains it and performs aggregate/flush off the
	// evaluation hot path. events is bounded; on overflow the hook drops the event and bumps
	// a counter — best-effort telemetry that never blocks the request.
	events     chan evalEvent
	workerDone chan struct{}
	enqueueMu  sync.RWMutex

	// Telemetry counters. Split so operators can tell why we lost data:
	//  - preQueueOverflow: capacity check saw the queue full BEFORE any copy work.
	//  - enqueueDropped:   racy loss at the send site (queue filled between check and send).
	//  - contextTruncatedByReason: per-cap counter incremented once per truncated event with the
	//    specific reason (max_context_fields, max_field_length, max_key_length). A single event
	//    may bump multiple reasons; each is at most one bump per event.
	preQueueOverflow         atomic.Int64
	enqueueDropped           atomic.Int64
	contextTruncatedByReason sync.Map // map[string]*atomic.Int64
}

// evalEvent is the bounded snapshot the Finally hook hands to the worker. The context has
// already been flattened, pruned, and copied on the evaluation thread, so what sits in the
// queue is already-bounded (256 fields × 256 chars) — the queue's memory footprint is
// bounded by cap(events) × pruned context size, not by cap(events) × caller context size.
type evalEvent struct {
	d                evalDetails
	contextAttrs     map[string]any
	evaluationTimeMs int64
}

// evalDetails holds extracted flag evaluation fields for EVP aggregation.
// Used only by flagEvalLoggingHook; does NOT replace extraction in flageval_metrics.go.
type evalDetails struct {
	flagKey        string
	variant        string
	allocationKey  string
	targetingKey   string
	errorMessage   string
	runtimeDefault bool
	evalTimeMs     int64
	// observeFullEvaluationData is the consent the evaluator stamped onto this evaluation's
	// metadata. Read only from there — never from live configuration.
	observeFullEvaluationData bool
}

// newFlagEvalLoggingWriter creates a new flag evaluation writer.
func newFlagEvalLoggingWriter(config ProviderConfig) *flagEvalLoggingWriter {
	return newFlagEvalLoggingWriterWithEVP(config, newEVPClient())
}

func newFlagEvalLoggingWriterWithEVP(config ProviderConfig, evp *evpClient) *flagEvalLoggingWriter {
	executable, _ := os.Executable()

	flushInterval := cmp.Or(config.FlagEvaluationFlushInterval, defaultFlagEvalFlushInterval)

	return &flagEvalLoggingWriter{
		flushInterval: flushInterval,
		evp:           evp,
		stopChan:      make(chan struct{}),
		workerDone:    make(chan struct{}),
		events:        make(chan evalEvent, defaultEvalEventBufferSize),
		ddContext: flagEvalDDContext{
			Service: cmp.Or(env.Get("DD_SERVICE"), globalconfig.ServiceName(), executable),
			Version: env.Get("DD_VERSION"),
			Env:     env.Get("DD_ENV"),
		},
		aggregator: flagEvalLoggingAggregator{
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
func (w *flagEvalLoggingWriter) start() {
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

		// Single owner of aggregate/flush. The hook enqueues only bounded snapshots.
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
func (w *flagEvalLoggingWriter) stop() {
	w.enqueueMu.Lock()
	// Single idempotency gate: the atomic Swap is the guard for both "mark stopped" and the
	// downstream close(stopChan). enqueueMu prevents a record() call that observed stopped=false
	// from sending into events after the worker has drained and exited.
	if w.stopped.Swap(true) {
		w.enqueueMu.Unlock()
		return
	}

	// Signal the worker to drain the queue and do a final flush.
	close(w.stopChan)
	w.enqueueMu.Unlock()
	if w.ticker != nil {
		// Worker was started: wait for its final flush before returning, then stop the
		// ticker. (ticker is set only in start(), so it gates "was the worker started".)
		<-w.workerDone
		w.ticker.Stop()
	}

	log.Debug("openfeature: flag evaluation writer stopped")
}

// flush drains the aggregator, assembles per-tier events, and sends them to the agent.
func (w *flagEvalLoggingWriter) flush() {
	// Surface best-effort backpressure drops (queue full) as observable signals. Split so
	// operators can tell "the queue was already full at hook time" (preQueueOverflow, hot
	// signal for undersized cap) from "the queue filled between the check and the send"
	// (enqueueDropped, cold signal for pathological contention).
	if d := w.preQueueOverflow.Swap(0); d > 0 {
		log.Debug("openfeature: flag evaluation queue full at hook time — dropped %d evaluation(s) before copy (best-effort telemetry)", d)
	}
	if d := w.enqueueDropped.Swap(0); d > 0 {
		log.Debug("openfeature: flag evaluation queue full at send — dropped %d evaluation(s) after copy (best-effort telemetry)", d)
	}
	w.contextTruncatedByReason.Range(func(k, v any) bool {
		if c := v.(*atomic.Int64).Swap(0); c > 0 {
			log.Debug("openfeature: flag evaluation context truncated by %s on %d evaluation(s) (best-effort telemetry)", k, c)
		}
		return true
	})

	events := w.buildFlushEvents(time.Now().UnixMilli())
	if len(events) == 0 {
		return
	}

	payload := flagEvalLoggingPayload{
		Context:         w.ddContext,
		FlagEvaluations: events,
	}

	if err := w.sendToAgent(payload); err != nil {
		log.Error("openfeature: failed to send flag evaluation events: %v", err.Error())
	} else {
		log.Debug("openfeature: successfully sent %d flag evaluation events", len(events))
	}
}

// buildFlushEvents drains both tiers and renders them as wire events stamped with flushTimeMs.
// Split from flush so the full-fidelity vs privacy-protected serialization can be tested
// without the transport.
func (w *flagEvalLoggingWriter) buildFlushEvents(flushTimeMs int64) []flagEvalLoggingEvent {
	w.aggregator.mu.Lock()

	// Under lock: drain both maps.
	full := w.aggregator.full
	degraded := w.aggregator.degraded

	// Surface degraded-overflow drops so an undersized degradedCap is observable rather than
	// a silent loss of legitimate counts.
	degradedOverflow := w.aggregator.droppedDegradedOverflow

	if (len(full) + len(degraded)) == 0 {
		w.aggregator.droppedDegradedOverflow = 0
		w.aggregator.mu.Unlock()
		if degradedOverflow > 0 {
			log.Warn("openfeature: degraded aggregation tier full — dropped %d evaluation(s); raise degradedCap (best-effort telemetry)", degradedOverflow)
		}
		return nil
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

	var events []flagEvalLoggingEvent

	// Full tier: required fields + variant + allocation + targeting_key + context + error.
	// runtime_default_used decorates this tier when the caller default was returned.
	for key, e := range full {
		ev := baseFlagEvalLoggingEvent(key.flagKey, e, flushTimeMs)
		ev.RuntimeDefault = e.runtimeDefault
		// Consent-on emits raw targeting_key + context; consent-off emits the hashed key and
		// omits context (absent, not empty). Consent is read from the bucket snapshot, never
		// from live configuration.
		if e.observeFullEvaluationData {
			ev.TargetingKey = e.targetingKey
			if len(e.contextAttrs) > 0 {
				ev.Context = &flagEvalEventContext{Evaluation: e.contextAttrs}
			}
		} else {
			ev.TargetingKey = hashTargetingKey(e.targetingKey)
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
		events = append(events, ev)
	}

	// Degraded tier: required fields + variant + allocation + error; no targeting_key, no context.
	// runtime_default_used decorates this tier when the caller default was returned.
	for key, e := range degraded {
		ev := baseFlagEvalLoggingEvent(key.flagKey, e, flushTimeMs)
		ev.RuntimeDefault = e.runtimeDefault
		if key.variant != "" {
			ev.Variant = &flagEvalVariant{Key: key.variant}
		}
		if key.allocationKey != "" {
			ev.Allocation = &flagEvalAllocation{Key: key.allocationKey}
		}
		if e.errorMessage != "" {
			ev.Error = &flagEvalError{Message: e.errorMessage}
		}
		events = append(events, ev)
	}

	return events
}

// baseFlagEvalLoggingEvent builds a flagEvalLoggingEvent with ONLY the five required schema
// fields (timestamp, flag.key, first/last evaluation, evaluation_count). It is tier-agnostic
// and sets no optional field — RuntimeDefault and the rest are decoration applied by each tier
// loop in flush() after the call.
func baseFlagEvalLoggingEvent(flagKey string, e *evaluationEntry, flushTimeMs int64) flagEvalLoggingEvent {
	return flagEvalLoggingEvent{
		Timestamp:       flushTimeMs,
		Flag:            flagEvalFlag{Key: flagKey},
		FirstEvaluation: e.firstEvaluation,
		LastEvaluation:  e.lastEvaluation,
		EvaluationCount: e.count,
	}
}

// record runs on the evaluation hot path (the Finally hook). Under consent-on it flattens
// and prunes the caller's context INTO a bounded snapshot before enqueueing, so the async
// queue holds only already-bounded contexts (256 fields × 256 chars × ≤256-char keys). Under
// consent-off the context is neither serialized nor keyed, so the copy is skipped entirely.
// The pre-enqueue placement is deliberate: deferring the copy to the worker goroutine would
// let an unbounded caller context inflate the queue and heap footprint before the drop-on-
// full check ever fires. Non-blocking on enqueue; failure paths bump split counters so an
// undersized cap surfaces as pre-queue overflow rather than getting hidden in a race counter.
func (w *flagEvalLoggingWriter) record(hookContext of.HookContext, details of.InterfaceEvaluationDetails) {
	w.enqueueMu.RLock()
	defer w.enqueueMu.RUnlock()

	// Post-stop no-op: after stop() the worker no longer drains w.events, so enqueuing would
	// silently lose the event. Check the atomic gate lock-free (reading under the aggregator
	// lock would add hot-path contention) and count the event as dropped so it stays observable.
	if w.stopped.Load() {
		w.preQueueOverflow.Add(1)
		return
	}
	if len(w.events) == cap(w.events) {
		// Queue full at hook time — O(1) drop, no copy work. This is the hot signal for an
		// undersized cap. Counted separately from the racy send-site loss below so the two
		// causes stay distinguishable.
		w.preQueueOverflow.Add(1)
		return
	}
	d := extractEvalDetails(hookContext, details)
	evaluationTimeMs := d.evalTimeMs
	if evaluationTimeMs == 0 {
		evaluationTimeMs = time.Now().UnixMilli()
	}
	ev := evalEvent{
		d:                d,
		evaluationTimeMs: evaluationTimeMs,
	}
	if d.observeFullEvaluationData {
		// Bound the context ON THE EVALUATION THREAD, before the async queue. The worker
		// then only ever sees the pruned snapshot, and the caller's context reference is
		// released as soon as record() returns.
		attrs, truncReasons := flattenAndPruneContext(hookContext.EvaluationContext().Attributes())
		ev.contextAttrs = attrs
		for _, reason := range truncReasons {
			w.incContextTruncated(reason)
		}
	}
	select {
	case w.events <- ev:
	default:
		w.enqueueDropped.Add(1)
	}
}

// aggregate updates the aggregator. It runs only on the writer's single worker goroutine.
// The caller's context has already been pruned on the evaluation thread (or omitted under
// consent-off), so nothing else needs to happen to bound the queue's memory footprint here.
func (w *flagEvalLoggingWriter) aggregate(ev evalEvent) {
	w.aggregator.add(ev.d, ev.contextAttrs, ev.evaluationTimeMs)
}

// incContextTruncated bumps the per-reason context-truncated counter. Reasons are the small
// set of truncReason* constants; each event may bump multiple reasons but each is bumped at
// most once per event.
func (w *flagEvalLoggingWriter) incContextTruncated(reason string) {
	c, ok := w.contextTruncatedByReason.Load(reason)
	if !ok {
		c, _ = w.contextTruncatedByReason.LoadOrStore(reason, new(atomic.Int64))
	}
	c.(*atomic.Int64).Add(1)
}

// flattenAndPruneContext produces the pruned context map for EVP aggregation in a single
// traversal of the flattened keyspace. It merges the
// two former steps — flattenContext (flatten.go) + pruneContext — into one pass with the SAME
// pruned output:
//
//  1. Flatten nested objects into a single-level dot-notation map (reusing flattenRecursive, so
//     the flatten semantics stay identical to the exposure path which still calls
//     flattenContext directly — that caller is unchanged).
//  2. Apply the deterministic prune: sort the flattened keys, then keep the first
//     maxContextFields whose keys and string values pass the length caps.
//
// The second return value is the SET of truncation reasons that fired, so the caller can bump
// telemetry counters (context-truncated by reason). Reasons in the returned slice are unique;
// callers should treat it as at most one bump per (event, reason).
//
// Allocation win: when the flattened context already fits every limit (the common case — few
// fields, short keys, no oversized string), the flattened map is returned DIRECTLY, so the
// separate pruned-output map is elided.
func flattenAndPruneContext(attrs map[string]any) (map[string]any, []string) {
	if len(attrs) == 0 {
		return nil, nil
	}
	if contextFitsWithoutFlattening(attrs) {
		return attrs, nil
	}

	flat := make(map[string]any, len(attrs))
	flattenRecursive("", attrs, flat)
	if len(flat) == 0 {
		return nil, nil
	}

	// Determine whether any pruning is actually required: an over-cap field count or any
	// oversized key/value. If neither, the flattened map already IS the pruned result.
	needsPrune := len(flat) > maxContextFields
	if !needsPrune {
		for k, v := range flat {
			if len(k) > maxKeyLength {
				needsPrune = true
				break
			}
			if s, ok := v.(string); ok && len(s) > maxFieldLength {
				needsPrune = true
				break
			}
		}
	}
	if !needsPrune {
		return flat, nil
	}

	// Deterministic prune: sort keys, then keep the first maxContextFields non-oversized values.
	// Sorting BEFORE the oversized-key/value skips and the field cap makes the kept subset
	// stable across calls (Go map iteration is randomized), so logically-identical contexts
	// always prune to the same subset and the same canonicalContextKey.
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]any, min(len(flat), maxContextFields))
	// Track which caps fired via a small bitset; the caller expands to reason strings.
	const (
		bitFields uint8 = 1 << iota
		bitValue
		bitKey
	)
	var truncBits uint8
	count := 0
	for _, k := range keys {
		if count >= maxContextFields {
			truncBits |= bitFields
			break
		}
		if len(k) > maxKeyLength {
			truncBits |= bitKey
			continue
		}
		v := flat[k]
		if s, ok := v.(string); ok && len(s) > maxFieldLength {
			truncBits |= bitValue
			continue
		}
		out[k] = v
		count++
	}

	var reasons []string
	if truncBits&bitFields != 0 {
		reasons = append(reasons, truncReasonMaxContextFields)
	}
	if truncBits&bitValue != 0 {
		reasons = append(reasons, truncReasonMaxFieldLength)
	}
	if truncBits&bitKey != 0 {
		reasons = append(reasons, truncReasonMaxKeyLength)
	}
	if len(out) == 0 {
		return nil, reasons
	}
	return out, reasons
}

func contextFitsWithoutFlattening(attrs map[string]any) bool {
	if len(attrs) > maxContextFields {
		return false
	}
	for k, v := range attrs {
		if len(k) > maxKeyLength {
			return false
		}
		switch x := v.(type) {
		case string:
			if len(x) > maxFieldLength {
				return false
			}
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64, bool:
		default:
			return false
		}
	}
	return true
}

// drainAndFlush processes any buffered events and performs a final flush. Called by the
// worker when stopping so a final batch is not lost on shutdown.
func (w *flagEvalLoggingWriter) drainAndFlush() {
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
func (w *flagEvalLoggingWriter) sendToAgent(payload flagEvalLoggingPayload) error {
	return w.evp.post(flagEvalLoggingEndpoint, "flag evaluation", payload)
}

// add records one evaluation observation into the appropriate aggregation tier.
// Must be called WITHOUT the aggregator lock held (it acquires the lock internally).
// Routes observations through full → degraded → drop(counted).
//
// Per-flag attempt counting: perFlagFull[flag] is incremented on every call for a flag
// (whether or not a full-tier bucket is actually created). This ensures that once
// globalCap is full, a flag that accumulates enough attempts (>= perFlagCap) still
// follows the degraded path — keeping the per-flag overflow path alive even after the
// global full-tier cap is exhausted.
func (a *flagEvalLoggingAggregator) add(d evalDetails, contextAttrs map[string]any, evaluationTimeMs int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Enforce the consent-off invariant for every caller: no context on the wire → no context
	// in the key (see evaluationAggregationKey).
	if !d.observeFullEvaluationData {
		contextAttrs = nil
	}

	// Build the full key from schema-visible dimensions including the canonical context encoding.
	// No hash, so distinct contexts get distinct buckets.
	fullKey := evaluationAggregationKey{
		flagKey:                   d.flagKey,
		variant:                   d.variant,
		allocationKey:             d.allocationKey,
		runtimeDefault:            d.runtimeDefault,
		errorMessage:              d.errorMessage,
		targetingKey:              d.targetingKey,
		contextKey:                canonicalContextKey(contextAttrs),
		observeFullEvaluationData: d.observeFullEvaluationData,
	}

	// Fast path: this exact full-tier bucket already exists → increment its count. Because
	// contextKey is the full canonical encoding (not a digest), this fast path is hit only by a
	// genuinely identical pruned context — never by an aliasing collision.
	if e, ok := a.full[fullKey]; ok {
		// Defense in depth: AND-fold so if consent ever leaves the key, one consent-off
		// observation still forces the whole bucket to the privacy-protected path.
		e.observeFullEvaluationData = e.observeFullEvaluationData && d.observeFullEvaluationData
		e.observe(evaluationTimeMs)
		return
	}

	// Check per-flag cap.
	if a.perFlagFull[d.flagKey] >= a.perFlagCap {
		// perFlagCap exceeded — route to degraded tier.
		a.addToDegraded(d, evaluationTimeMs)
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
		// cardinality at the target scale. The per-flag attempt counter was
		// already incremented above; once it reaches perFlagCap this flag routes through
		// addToDegraded directly as well.
		a.addToDegraded(d, evaluationTimeMs)
		return
	}

	// New full-tier entry.
	a.full[fullKey] = &evaluationEntry{
		count:                     1,
		firstEvaluation:           evaluationTimeMs,
		lastEvaluation:            evaluationTimeMs,
		runtimeDefault:            d.runtimeDefault,
		targetingKey:              d.targetingKey,
		contextAttrs:              contextAttrs,
		errorMessage:              d.errorMessage,
		observeFullEvaluationData: d.observeFullEvaluationData,
	}
	a.globalCount++
}

// addToDegraded adds an entry to the degraded map (drops targeting_key + context).
// Called with the aggregator lock held. When a NEW degraded bucket would exceed degradedCap,
// the evaluation's count is DROPPED and counted (droppedDegradedOverflow). degradedCap is
// sized (defaultEvalDegradedCap) to hold the legitimate degraded cardinality at the target
// scale, so this drop only fires under cardinality far beyond that target (e.g. an unbounded
// dynamic/abusive flag key) and the dropped counter makes such overflow observable.
func (a *flagEvalLoggingAggregator) addToDegraded(d evalDetails, evaluationTimeMs int64) {
	degKey := evaluationDegradedKey{
		flagKey:        d.flagKey,
		variant:        d.variant,
		allocationKey:  d.allocationKey,
		runtimeDefault: d.runtimeDefault,
		errorMessage:   d.errorMessage,
	}

	if e, ok := a.degraded[degKey]; ok {
		e.observe(evaluationTimeMs)
		return
	}

	// New degraded bucket — check degradedCap.
	if len(a.degraded) >= a.degradedCap {
		// degradedCap exceeded — terminal tier full. Drop the count but keep it observable so an
		// undersized cap surfaces in the flush warning instead of silently losing legitimate data.
		a.droppedDegradedOverflow++
		return
	}

	e := newEvaluationEntry(evaluationTimeMs)
	e.runtimeDefault = d.runtimeDefault
	e.errorMessage = d.errorMessage
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
// full-tier buckets. The returned string is stored once per full-tier bucket.
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
// allocation for the common scalar types; rare/complex types fall back to a type-qualified
// fmt rendering. The encoding only needs to be deterministic within a run and collision-free
// across distinct values.
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
		tmp = fmt.Appendf(tmp, "%T:%v", x, x)
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
// This helper is used only by flagEvalLoggingHook — it does NOT replace the extraction in
// flageval_metrics.go (that file is left untouched to preserve the OTel path).
func extractEvalDetails(hookContext of.HookContext, details of.InterfaceEvaluationDetails) evalDetails {
	allocationKey, _ := details.FlagMetadata[metadataAllocationKey].(string)
	evalTimeMs, _ := details.FlagMetadata[metadataEvalTimeKey].(int64)
	// Fail closed: a missing or non-bool consent stamp yields false, so an evaluation that
	// never passed through the evaluator cannot emit raw PII.
	observeFullEvaluationData, _ := details.FlagMetadata[metadataObserveFullEvaluationDataKey].(bool)
	// error.message can carry raw evaluation-context values verbatim: our own evaluator wraps
	// user-supplied attributes in error strings (variant/type mismatches, regex/version parse
	// errors), and third-party providers can do the same. Under consent-off we drop the raw
	// text and substitute ErrorCode so operators keep a stable, non-PII signal. Redacted here
	// so the raw string never enters the aggregation key or the async writer queue — both live
	// beyond the evaluation call and would leak PII if we deferred redaction to emit time.
	errMsg := details.ErrorMessage
	if !observeFullEvaluationData {
		if details.ErrorCode != "" {
			errMsg = string(details.ErrorCode)
		} else {
			errMsg = ""
		}
	} else if errMsg == "" && details.ErrorCode != "" {
		// Consent-on fallback preserved: some providers populate only ErrorCode.
		errMsg = string(details.ErrorCode)
	}
	return evalDetails{
		flagKey:                   hookContext.FlagKey(),
		variant:                   details.Variant,
		allocationKey:             allocationKey,
		targetingKey:              hookContext.EvaluationContext().TargetingKey(),
		errorMessage:              errMsg,
		runtimeDefault:            isRuntimeDefault(details),
		evalTimeMs:                evalTimeMs,
		observeFullEvaluationData: observeFullEvaluationData,
	}
}
