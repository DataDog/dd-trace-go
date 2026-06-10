// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

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
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const (
	defaultEvaluationFlushInterval = 10 * time.Second
	evaluationEndpoint             = "/evp_proxy/v2/api/v2/flagevaluations"
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

// evaluationAggregationKey is the full-fidelity aggregation key.
// Matches the (flag, variant, allocation, rule, reason, subject, context) tuple the browser SDK uses.
type evaluationAggregationKey struct {
	flagKey          string
	variant          string
	allocationKey    string
	targetingRuleKey string
	reason           string
	targetingKey     string
	contextHash      uint64
}

// evaluationDegradedKey is the rollup key used when the per-flag cap is exceeded.
// It drops subject/context dimensions, preserving only (flag, variant, allocation, rule, reason).
type evaluationDegradedKey struct {
	flagKey          string
	variant          string
	allocationKey    string
	targetingRuleKey string
	reason           string
}

// evaluationEntry holds per-window count and timestamps for one aggregation bucket.
type evaluationEntry struct {
	count           int64
	firstEvaluation int64
	lastEvaluation  int64
	// Full-fidelity fields (nil for degraded entries)
	targetingKey   string
	contextAttrs   map[string]any
	errorType      string
	errorMessage   string
	runtimeDefault bool
}

// hashKey computes a stable FNV-1a 64-bit hash for the given aggregation key.
// Called outside the aggregator mutex to minimize lock hold time.
func hashKey(k evaluationAggregationKey) uint64 {
	h := fnv.New64a()
	for _, s := range []string{k.flagKey, k.variant, k.allocationKey, k.targetingRuleKey, k.reason, k.targetingKey} {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	var buf [8]byte
	buf[0] = byte(k.contextHash)
	buf[1] = byte(k.contextHash >> 8)
	buf[2] = byte(k.contextHash >> 16)
	buf[3] = byte(k.contextHash >> 24)
	buf[4] = byte(k.contextHash >> 32)
	buf[5] = byte(k.contextHash >> 40)
	buf[6] = byte(k.contextHash >> 48)
	buf[7] = byte(k.contextHash >> 56)
	_, _ = h.Write(buf[:])
	return h.Sum64()
}

// hashDegradedKey computes an FNV-1a hash for the degraded (rollup) key.
func hashDegradedKey(k evaluationDegradedKey) uint64 {
	h := fnv.New64a()
	for _, s := range []string{k.flagKey, k.variant, k.allocationKey, k.targetingRuleKey, k.reason} {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// hashContext hashes the primitive evaluation context attributes for use in the aggregation key.
// Sorts keys for determinism, skips non-primitive values.
func hashContext(attrs map[string]any) uint64 {
	if len(attrs) == 0 {
		return 0
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(primitiveToString(attrs[k])))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// primitiveToString converts a primitive value to its string representation for hashing.
func primitiveToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(val)
	case int8:
		return strconv.FormatInt(int64(val), 10)
	case int16:
		return strconv.FormatInt(int64(val), 10)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	case uint8:
		return strconv.FormatUint(uint64(val), 10)
	case uint16:
		return strconv.FormatUint(uint64(val), 10)
	case uint32:
		return strconv.FormatUint(uint64(val), 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return ""
	}
}

type evaluationAggregator struct {
	mu           sync.Mutex
	full         map[uint64]*evaluationEntry
	degraded     map[uint64]*evaluationEntry
	keys         map[uint64]evaluationAggregationKey  // parallel to full
	degradedKeys map[uint64]evaluationDegradedKey     // parallel to degraded
	perFlagFull  map[string]int
	perFlagCap   int
	globalCap    int
	globalCount  int
}

func newEvaluationAggregator(perFlagCap, globalCap int) *evaluationAggregator {
	return &evaluationAggregator{
		full:         make(map[uint64]*evaluationEntry),
		degraded:     make(map[uint64]*evaluationEntry),
		keys:         make(map[uint64]evaluationAggregationKey),
		degradedKeys: make(map[uint64]evaluationDegradedKey),
		perFlagFull:  make(map[string]int),
		perFlagCap:   perFlagCap,
		globalCap:    globalCap,
	}
}

// add records a single flag evaluation. The hash is computed outside the lock to minimise contention.
func (a *evaluationAggregator) add(key evaluationAggregationKey, contextAttrs map[string]any, errorType, errorMessage string, runtimeDefault bool, nowMs int64) {
	h := hashKey(key)

	a.mu.Lock()
	defer a.mu.Unlock()

	// Fast path: existing full-fidelity entry.
	if e, ok := a.full[h]; ok {
		e.count++
		e.lastEvaluation = nowMs
		return
	}

	// New tuple for this flag — check per-flag cap.
	if a.perFlagFull[key.flagKey] >= a.perFlagCap {
		// Route to degraded (rollup) bucket.
		dk := evaluationDegradedKey{
			flagKey:          key.flagKey,
			variant:          key.variant,
			allocationKey:    key.allocationKey,
			targetingRuleKey: key.targetingRuleKey,
			reason:           key.reason,
		}
		dh := hashDegradedKey(dk)
		if de, ok := a.degraded[dh]; ok {
			de.count++
			de.lastEvaluation = nowMs
		} else {
			a.degraded[dh] = &evaluationEntry{
				count:           1,
				firstEvaluation: nowMs,
				lastEvaluation:  nowMs,
			}
			a.degradedKeys[dh] = dk
		}
		// Do NOT increment perFlagFull or globalCount for degraded inserts.
		return
	}

	// Check global cap before inserting into full.
	if a.globalCount >= a.globalCap {
		// Fairness eviction: if this flag has never been seen this window, evict one entry
		// from the noisiest flag so every flag gets at least one full-fidelity event.
		if a.perFlagFull[key.flagKey] == 0 {
			// Find victim flag: the flag with the highest perFlagFull count.
			var victimFlag string
			var victimCount int
			for f, c := range a.perFlagFull {
				if c > victimCount {
					victimCount = c
					victimFlag = f
				}
			}
			if victimFlag != "" {
				// Find any full entry belonging to the victim flag.
				var evictedHash uint64
				var found bool
				for h, k := range a.keys {
					if k.flagKey == victimFlag {
						evictedHash = h
						found = true
						break
					}
				}
				if found {
					evictedKey := a.keys[evictedHash]
					evictedEntry := a.full[evictedHash]

					// Build degraded key from evicted entry and fold count.
					dk := evaluationDegradedKey{
						flagKey:          evictedKey.flagKey,
						variant:          evictedKey.variant,
						allocationKey:    evictedKey.allocationKey,
						targetingRuleKey: evictedKey.targetingRuleKey,
						reason:           evictedKey.reason,
					}
					dh := hashDegradedKey(dk)
					if de, ok := a.degraded[dh]; ok {
						de.count += evictedEntry.count
						if evictedEntry.lastEvaluation > de.lastEvaluation {
							de.lastEvaluation = evictedEntry.lastEvaluation
						}
					} else {
						a.degraded[dh] = &evaluationEntry{
							count:           evictedEntry.count,
							firstEvaluation: evictedEntry.firstEvaluation,
							lastEvaluation:  evictedEntry.lastEvaluation,
						}
						a.degradedKeys[dh] = dk
					}

					// Remove evicted entry from full.
					delete(a.full, evictedHash)
					delete(a.keys, evictedHash)
					a.perFlagFull[victimFlag]--
					a.globalCount--
					// Fall through to insert the cold flag below.
				}
			}
		}

		// If still at cap (no eviction happened or victim not found), route to degraded.
		if a.globalCount >= a.globalCap {
			dk := evaluationDegradedKey{
				flagKey:          key.flagKey,
				variant:          key.variant,
				allocationKey:    key.allocationKey,
				targetingRuleKey: key.targetingRuleKey,
				reason:           key.reason,
			}
			dh := hashDegradedKey(dk)
			if de, ok := a.degraded[dh]; ok {
				de.count++
				de.lastEvaluation = nowMs
			} else {
				a.degraded[dh] = &evaluationEntry{
					count:           1,
					firstEvaluation: nowMs,
					lastEvaluation:  nowMs,
				}
				a.degradedKeys[dh] = dk
			}
			// Do NOT increment perFlagFull or globalCount for degraded inserts.
			return
		}
	}

	// Under cap: insert into full.
	a.full[h] = &evaluationEntry{
		count:           1,
		firstEvaluation: nowMs,
		lastEvaluation:  nowMs,
		targetingKey:    key.targetingKey,
		contextAttrs:    contextAttrs,
		errorType:       errorType,
		errorMessage:    errorMessage,
		runtimeDefault:  runtimeDefault,
	}
	a.keys[h] = key
	a.perFlagFull[key.flagKey]++
	a.globalCount++
}

// drain swaps full and degraded maps with fresh ones, resets counters, and returns the old maps.
func (a *evaluationAggregator) drain() (
	full map[uint64]*evaluationEntry,
	degraded map[uint64]*evaluationEntry,
	keys map[uint64]evaluationAggregationKey,
	degradedKeys map[uint64]evaluationDegradedKey,
) {
	a.mu.Lock()
	defer a.mu.Unlock()

	full = a.full
	degraded = a.degraded
	keys = a.keys
	degradedKeys = a.degradedKeys
	a.full = make(map[uint64]*evaluationEntry)
	a.degraded = make(map[uint64]*evaluationEntry)
	a.keys = make(map[uint64]evaluationAggregationKey)
	a.degradedKeys = make(map[uint64]evaluationDegradedKey)
	a.perFlagFull = make(map[string]int)
	a.globalCount = 0
	return full, degraded, keys, degradedKeys
}

// flattenAndExtractPrimitive flattens the OpenFeature evaluation context and returns
// only primitive-typed attributes in a single map. One allocation total.
func flattenAndExtractPrimitive(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	result := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if strings.HasPrefix(k, "targetingKey") {
			continue
		}
		switch v.(type) {
		case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// evaluationWriter manages buffering and flushing of flag evaluation count events to the Datadog Agent.
type evaluationWriter struct {
	aggregator    *evaluationAggregator
	flushInterval time.Duration
	httpClient    *http.Client
	agentURL      *url.URL
	context       evaluationDDContext
	ticker        *time.Ticker
	stopChan      chan struct{}
	mu            sync.Mutex
	stopped       bool
	jsonConfig    jsoniter.API
}

// newEvaluationWriter creates a new evaluation writer with the given configuration.
// Returns nil if the kill switch DD_FLAGGING_EVALUATION_COUNTS_ENABLED=false.
func newEvaluationWriter(config ProviderConfig) *evaluationWriter {
	if !internal.BoolEnv(envEvaluationCountsEnabled, true) {
		return nil
	}

	perFlagCap := internal.IntEnv(envEvaluationPerFlagCap, defaultEvaluationPerFlagCap)
	globalCap := internal.IntEnv(envEvaluationGlobalCap, defaultEvaluationGlobalCap)

	agentURL := internal.AgentURLFromEnv()
	var httpClient *http.Client
	if agentURL.Scheme == "unix" {
		httpClient = internal.UDSClient(agentURL.Path, defaultHTTPTimeout)
		agentURL = internal.UnixDataSocketURL(agentURL.Path)
	} else {
		httpClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
	}

	executable, _ := os.Executable()

	return &evaluationWriter{
		aggregator:    newEvaluationAggregator(perFlagCap, globalCap),
		flushInterval: cmp.Or(config.EvaluationFlushInterval, defaultEvaluationFlushInterval),
		httpClient:    httpClient,
		agentURL:      agentURL,
		stopChan:      make(chan struct{}),
		jsonConfig:    jsoniter.Config{}.Froze(),
		context: evaluationDDContext{
			Service: cmp.Or(env.Get("DD_SERVICE"), globalconfig.ServiceName(), executable),
			Version: env.Get("DD_VERSION"),
			Env:     env.Get("DD_ENV"),
		},
	}
}

// start begins the periodic flushing of flag evaluation count events.
func (w *evaluationWriter) start() {
	w.ticker = time.NewTicker(w.flushInterval)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("openfeature: evaluation writer recovered panic: %s", r)
				var errAttr slog.Attr
				if err, ok := r.(error); ok {
					errAttr = slog.Any("panic", telemetrylog.NewSafeError(err))
				} else {
					errAttr = slog.Any("panic", r)
				}
				telemetrylog.Error("openfeature: evaluation writer recovered panic", errAttr)
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

// flush drains the aggregator and sends batched evaluation events to the agent.
func (w *evaluationWriter) flush() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	full, degraded, keys, degradedKeys := w.aggregator.drain()
	w.mu.Unlock()

	if len(full) == 0 && len(degraded) == 0 {
		return
	}

	events := buildEvaluationEvents(full, degraded, keys, degradedKeys)
	if len(events) == 0 {
		return
	}

	if err := w.sendToAgent(evaluationPayload{
		Context:         w.context,
		FlagEvaluations: events,
	}); err != nil {
		log.Error("openfeature: failed to send evaluation events: %v", err.Error())
	} else {
		log.Debug("openfeature: successfully sent %d evaluation events", len(events))
	}
}

// buildEvaluationEvents converts drained aggregator state into a slice of evaluationEvent.
func buildEvaluationEvents(
	full map[uint64]*evaluationEntry,
	degraded map[uint64]*evaluationEntry,
	keys map[uint64]evaluationAggregationKey,
	degradedKeys map[uint64]evaluationDegradedKey,
) []evaluationEvent {
	now := time.Now().UnixMilli()
	events := make([]evaluationEvent, 0, len(full)+len(degraded))

	for h, entry := range full {
		k := keys[h]
		ev := evaluationEvent{
			Flag:               evaluationFlag{Key: k.flagKey},
			EvaluationCount:    entry.count,
			FirstEvaluation:    entry.firstEvaluation,
			LastEvaluation:     entry.lastEvaluation,
			Timestamp:          now,
			Reason:             k.reason,
			TargetingKey:       entry.targetingKey,
			RuntimeDefaultUsed: entry.runtimeDefault,
		}
		if k.variant != "" {
			ev.Variant = &evaluationVariant{Key: k.variant}
		}
		if k.allocationKey != "" {
			ev.Allocation = &evaluationAllocation{Key: k.allocationKey}
		}
		if k.targetingRuleKey != "" {
			ev.TargetingRule = &evaluationTargetingRule{Key: k.targetingRuleKey}
		}
		if entry.contextAttrs != nil {
			ev.Context = &evaluationEventContext{
				Evaluation: entry.contextAttrs,
				DD:         nil,
			}
		}
		if entry.errorType != "" || entry.errorMessage != "" {
			ev.Error = &evaluationError{
				Type:    entry.errorType,
				Message: entry.errorMessage,
			}
		}
		events = append(events, ev)
	}

	for h, entry := range degraded {
		dk := degradedKeys[h]
		ev := evaluationEvent{
			Flag:            evaluationFlag{Key: dk.flagKey},
			EvaluationCount: entry.count,
			FirstEvaluation: entry.firstEvaluation,
			LastEvaluation:  entry.lastEvaluation,
			Timestamp:       now,
			Reason:          dk.reason,
		}
		if dk.variant != "" {
			ev.Variant = &evaluationVariant{Key: dk.variant}
		}
		if dk.allocationKey != "" {
			ev.Allocation = &evaluationAllocation{Key: dk.allocationKey}
		}
		if dk.targetingRuleKey != "" {
			ev.TargetingRule = &evaluationTargetingRule{Key: dk.targetingRuleKey}
		}
		events = append(events, ev)
	}

	return events
}

// sendToAgent sends the evaluation payload to the Datadog Agent via EVP proxy.
func (w *evaluationWriter) sendToAgent(payload evaluationPayload) error {
	var bytesBuffer bytes.Buffer
	encoder := w.jsonConfig.NewEncoder(&bytesBuffer)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("failed to encode evaluation payload: %w", err)
	}

	u := *w.agentURL
	u.Path = evaluationEndpoint
	requestURL := u.String()

	req, err := http.NewRequestWithContext(context.Background(), "POST", requestURL, &bytesBuffer)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(evpSubdomainHeader, evpSubdomainValue)
	req.Header.Set("DD-EVP-ORIGIN", "dd-trace-go")
	req.Header.Set("DD-EVP-ORIGIN-VERSION", version.Tag)

	log.Debug("openfeature: sending evaluation events to %s", requestURL)

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

// stop stops the evaluation writer.
func (w *evaluationWriter) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.mu.Unlock()

	close(w.stopChan)

	if w.ticker != nil {
		w.ticker.Stop()
	}

	log.Debug("openfeature: evaluation writer stopped")
}
