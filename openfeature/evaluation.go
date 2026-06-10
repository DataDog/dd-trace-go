// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
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
	targetingKey string
	contextAttrs map[string]any
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
