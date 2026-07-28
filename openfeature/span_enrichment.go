// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	_ "unsafe" // for go:linkname

	of "github.com/open-feature/go-sdk/openfeature"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	iof "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

type spanEnrichmentHook struct {
	of.UnimplementedHook
}

// check that we implement hook interface
var _ of.Hook = (*spanEnrichmentHook)(nil)

//go:linkname recordFFEEvaluation github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.recordFFEEvaluation
func recordFFEEvaluation(s *tracer.Span, eval *iof.FeatureFlagEvaluation)

func newSpanEnrichmentHook() *spanEnrichmentHook {
	return &spanEnrichmentHook{}
}

func (h *spanEnrichmentHook) Finally(ctx context.Context, hookContext of.HookContext, evalDetails of.InterfaceEvaluationDetails, hints of.HookHints) {
	span, ok := tracer.SpanFromContext(ctx)
	if span == nil || !ok {
		return
	}

	root := span.Root()
	if root == nil {
		return
	}

	eval := buildEvaluation(hookContext.EvaluationContext(), evalDetails)
	if eval == nil {
		return
	}

	recordFFEEvaluation(root, eval)
}

// buildEvaluation extracts a FeatureFlagEvaluation from OpenFeature hook data.
func buildEvaluation(evalCtx of.EvaluationContext, evalDetails of.InterfaceEvaluationDetails) *iof.FeatureFlagEvaluation {
	if raw, ok := evalDetails.FlagMetadata[metadataSerialIDKey]; ok {
		sid, ok := raw.(uint32)
		if !ok {
			log.Debug("openfeature: span enrichment: malformed serial id in metadata")
			return nil
		}
		eval := &iof.FeatureFlagEvaluation{
			FlagKey:  evalDetails.FlagKey,
			SerialID: &sid,
		}
		if doLog, err := evalDetails.FlagMetadata.GetBool(metadataDoLogKey); err == nil && doLog {
			eval.Subject = evalCtx.TargetingKey()
		}
		return eval
	} else if evalDetails.Variant == "" {
		// Runtime default was used.
		return &iof.FeatureFlagEvaluation{
			FlagKey:      evalDetails.FlagKey,
			DefaultValue: evalDetails.Value,
		}
	}
	return nil
}
