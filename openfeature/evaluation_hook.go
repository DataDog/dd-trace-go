// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"strings"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
)

const (
	metadataTargetingRuleKey = "dd.targeting.rule.key"
)

type evaluationHook struct {
	of.UnimplementedHook
	writer *evaluationWriter
}

func newEvaluationHook(writer *evaluationWriter) *evaluationHook {
	return &evaluationHook{writer: writer}
}

func (h *evaluationHook) After(
	ctx context.Context,
	hookContext of.HookContext,
	flagEvaluationDetails of.InterfaceEvaluationDetails,
	_ of.HookHints,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if h.writer == nil {
		return nil
	}

	metadata := flagEvaluationDetails.FlagMetadata

	var allocationKey string
	if metadata != nil {
		if v, ok := metadata[metadataAllocationKey]; ok {
			if s, ok := v.(string); ok {
				allocationKey = s
			}
		}
	}

	var targetingRuleKey string
	if metadata != nil {
		if v, ok := metadata[metadataTargetingRuleKey]; ok {
			if s, ok := v.(string); ok {
				targetingRuleKey = s
			}
		}
	}

	evalContext := hookContext.EvaluationContext()
	targetingKey := evalContext.TargetingKey()
	contextAttrs := flattenAndExtractPrimitive(evalContext.Attributes())
	contextHash := hashContext(contextAttrs)

	reason := internReason(flagEvaluationDetails.Reason)

	var errorType, errorMessage string
	if flagEvaluationDetails.ErrorCode != "" {
		errorType = strings.ToLower(string(flagEvaluationDetails.ErrorCode))
		errorMessage = flagEvaluationDetails.ErrorMessage
	}

	key := evaluationAggregationKey{
		flagKey:          hookContext.FlagKey(),
		variant:          flagEvaluationDetails.Variant,
		allocationKey:    allocationKey,
		targetingRuleKey: targetingRuleKey,
		reason:           reason,
		targetingKey:     targetingKey,
		contextHash:      contextHash,
	}

	h.writer.aggregator.add(key, contextAttrs, errorType, errorMessage, false, time.Now().UnixMilli())
	return nil
}

// internReason maps an OpenFeature Reason to the lowercase string representation
// used in evaluation events. Known reasons are returned as stable constants.
func internReason(r of.Reason) string {
	switch r {
	case of.TargetingMatchReason:
		return "targeting_match"
	case of.DefaultReason:
		return "default"
	case of.ErrorReason:
		return "error"
	case of.DisabledReason:
		return "disabled"
	case of.StaticReason:
		return "static"
	case of.CachedReason:
		return "cached"
	case of.SplitReason:
		return "split"
	default:
		return strings.ToLower(string(r))
	}
}
