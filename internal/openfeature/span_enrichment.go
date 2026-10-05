// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// This file implements temporary span enrichment for feature flag evaluations.
// Evaluations are collected while a traced request is in progress, stored here
// keyed by the owning span, and drained by the tracer when that span finishes.
// The drained enrichment is then encoded as span tags.
//
// The store is global rather than tracer-owned so evaluations survive tracer
// swaps between evaluation and span finish. The key is intentionally typed as
// any to avoid importing ddtrace/tracer and creating an import cycle; callers use
// *ddtrace/tracer.Span as the key.
//
// SpanEnrichment is not safe for concurrent use. Callers must synchronize calls
// to AddSpanEnrichment and DrainSpanEnrichment with the key owner's lifecycle
// lock. For tracer spans, ddtrace/tracer/span.go holds Span.mu and checks
// Span.finished before adding, then drains while holding the same lock during
// Finish.

// FeatureFlagEvaluation represents a single feature flag evaluation to be
// recorded in span enrichment.
type FeatureFlagEvaluation struct {
	FlagKey string
	// SerialId is the optional serial ID from flag metadata. Nil for runtime defaults.
	SerialID *uint32
	// Subject is the targeting key / subject of experiment. Non-empty only when evaluation
	// should be logged (doLog=true).
	Subject string
	// DefaultValue is the default value used. Non-nil only when a runtime default was used.
	DefaultValue any
}

const (
	spanEnrichmentMaxSerialIDs           = 200
	spanEnrichmentMaxSubjects            = 10
	spanEnrichmentMaxSerialIDsPerSubject = 20
	spanEnrichmentMaxRuntimeDefaults     = 5
	spanEnrichmentMaxDefaultValueLen     = 64
)

// spanEnrichments stores pending span enrichments keyed by an owner
// object (*ddtrace/tracer.Span).
var spanEnrichments sync.Map

// AddSpanEnrichment records eval in the global span enrichment store.
func AddSpanEnrichment(key any, eval *FeatureFlagEvaluation) {
	if key == nil || eval == nil {
		return
	}

	// Two phase Load+LoadOrStore to avoid allocating a
	// SpanEnrichment on the hot path (entry already present) that
	// LoadOrStore would require.
	var enrichment *SpanEnrichment
	if actual, ok := spanEnrichments.Load(key); ok {
		enrichment = actual.(*SpanEnrichment)
	} else {
		e, _ := spanEnrichments.LoadOrStore(key, newSpanEnrichment())
		enrichment = e.(*SpanEnrichment)
	}
	enrichment.addEvaluation(eval)
}

// DrainSpanEnrichment removes and returns enrichment from the global span
// enrichment store.
func DrainSpanEnrichment(key any) *SpanEnrichment {
	if key == nil {
		return nil
	}
	v, ok := spanEnrichments.LoadAndDelete(key)
	if !ok {
		return nil
	}
	return v.(*SpanEnrichment)
}

// SpanEnrichment accumulates feature flag evaluations to be encoded as span tags.
//
// SpanEnrichment is not safe for concurrent use on its own. Thread safety is
// provided externally: every caller must hold the owning span's mu
// (ddtrace/tracer.Span.mu) before calling any method.
type SpanEnrichment struct {
	serialIDs       map[uint32]struct{}
	subjects        map[string]map[uint32]struct{}
	runtimeDefaults map[string]string
}

func newSpanEnrichment() *SpanEnrichment {
	return &SpanEnrichment{
		serialIDs:       make(map[uint32]struct{}),
		subjects:        make(map[string]map[uint32]struct{}),
		runtimeDefaults: make(map[string]string),
	}
}

// addEvaluation records a single feature flag evaluation.
// The caller must hold the owning span's mu.
func (se *SpanEnrichment) addEvaluation(eval *FeatureFlagEvaluation) {
	if eval == nil {
		return
	}

	if eval.SerialID != nil {
		se.addSerialID(*eval.SerialID)
		if eval.Subject != "" {
			se.addSubject(eval.Subject, *eval.SerialID)
		}
	} else if eval.DefaultValue != nil {
		se.addRuntimeDefault(eval.FlagKey, eval.DefaultValue)
	}
}

// Must be called with the owning span's mu held.
func (se *SpanEnrichment) addSerialID(sid uint32) {
	if _, exists := se.serialIDs[sid]; !exists {
		if len(se.serialIDs) >= spanEnrichmentMaxSerialIDs {
			log.Debug("openfeature: span enrichment: too many flag serial IDs, dropping (max %d)", spanEnrichmentMaxSerialIDs)
			return
		}
		se.serialIDs[sid] = struct{}{}
	}
}

// Must be called with the owning span's mu held.
func (se *SpanEnrichment) addSubject(subject string, sid uint32) {
	subjectIDs, ok := se.subjects[subject]
	if !ok {
		if len(se.subjects) >= spanEnrichmentMaxSubjects {
			log.Debug("openfeature: span enrichment: too many targeting keys, dropping (max %d)", spanEnrichmentMaxSubjects)
			return
		}
		subjectIDs = make(map[uint32]struct{})
		se.subjects[subject] = subjectIDs
	}
	if len(subjectIDs) >= spanEnrichmentMaxSerialIDsPerSubject {
		log.Debug("openfeature: span enrichment: too many experiments for subject %q, dropping (max %d)", subject, spanEnrichmentMaxSerialIDsPerSubject)
		return
	}
	subjectIDs[sid] = struct{}{}
}

// Must be called with the owning span's mu held.
func (se *SpanEnrichment) addRuntimeDefault(flagKey string, defaultValue any) {
	if _, exists := se.runtimeDefaults[flagKey]; exists {
		return
	}
	if len(se.runtimeDefaults) >= spanEnrichmentMaxRuntimeDefaults {
		log.Debug("openfeature: span enrichment: too many runtime defaults, dropping (max %d)", spanEnrichmentMaxRuntimeDefaults)
		return
	}
	var valueStr string
	if v, ok := defaultValue.(string); ok {
		valueStr = v
	} else if b, err := json.Marshal(defaultValue); err == nil {
		valueStr = string(b)
	} else {
		log.Debug("openfeature: span enrichment: failed to marshal runtime default value for key %q: %v", flagKey, err.Error())
		return
	}
	if len(valueStr) > spanEnrichmentMaxDefaultValueLen {
		log.Debug("openfeature: span enrichment: runtime default value for key %q exceeds max length (%d), truncating", flagKey, spanEnrichmentMaxDefaultValueLen)
		valueStr = valueStr[:spanEnrichmentMaxDefaultValueLen]
	}
	se.runtimeDefaults[flagKey] = strings.ToValidUTF8(valueStr, "")
}

// GetSpanTags returns span tags encoding accumulated feature flag evaluations.
// The caller must hold the owning span's mu.
func (se *SpanEnrichment) GetSpanTags() map[string]string {
	tags := make(map[string]string, 3)

	if len(se.serialIDs) > 0 {
		tags["ffe_flags_enc"] = encodeSerialIDs(se.serialIDs)
	}

	if len(se.subjects) > 0 {
		subjects := make(map[string]string, len(se.subjects))
		for key, ids := range se.subjects {
			sum := sha256.Sum256([]byte(key))
			hashKey := hex.EncodeToString(sum[:])
			subjects[hashKey] = encodeSerialIDs(ids)
		}
		if b, err := json.Marshal(subjects); err == nil {
			tags["ffe_subjects_enc"] = string(b)
		} else {
			log.Debug("openfeature: span enrichment: failed to marshal subjects: %v", err.Error())
		}
	}

	if len(se.runtimeDefaults) > 0 {
		defaults := se.runtimeDefaults
		if b, err := json.Marshal(defaults); err == nil {
			tags["ffe_runtime_defaults"] = string(b)
		} else {
			log.Debug("openfeature: span enrichment: failed to marshal runtime defaults: %v", err.Error())
		}
	}

	return tags
}

// Encode a set of serial ids using unsigned LEB128 delta encoding, wrapped in base64.
func encodeSerialIDs(ids map[uint32]struct{}) string {
	seq := make([]uint32, 0, len(ids))
	for id := range ids {
		seq = append(seq, id)
	}

	slices.Sort(seq)

	b := make([]byte, 0, 5*len(seq)) // 5 bytes is absolute max to encode an id
	var prevID uint32 = 0
	for _, id := range seq {
		diff := id - prevID
		prevID = id

		// Unsigned LEB128 encoding.
		for diff > 0x7f {
			b = append(b, byte((diff&0x7f)|0x80))
			diff >>= 7
		}
		b = append(b, byte(diff&0x7f))
	}

	return base64.StdEncoding.EncodeToString(b)
}
