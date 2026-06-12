// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"

	of "github.com/open-feature/go-sdk/openfeature"
)

// flagEvaluationHook implements the OpenFeature Hook interface to record EVP flagevaluation events.
// It uses the Finally hook stage (same as flagEvalHook) to cover success, error, and default paths.
// This satisfies CONT-09: Finally fires on error/default paths, unlike After (PR #4874 defect).
type flagEvaluationHook struct {
	of.UnimplementedHook
	writer *flagEvaluationWriter
}

// newFlagEvaluationHook creates a new EVP flag evaluation hook.
func newFlagEvaluationHook(w *flagEvaluationWriter) *flagEvaluationHook {
	return &flagEvaluationHook{writer: w}
}

// Finally is called after every flag evaluation (success or error).
// Using Finally (not After) ensures error-path and provider-not-ready evaluations are counted (CONT-09).
// Mirrors flageval_metrics.go's Finally stage; the EVP hook is a separate registered hook (D-06).
func (h *flagEvaluationHook) Finally(
	ctx context.Context,
	hookContext of.HookContext,
	details of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if h.writer == nil {
		return
	}
	h.writer.record(hookContext, details)
}

// isRuntimeDefault returns true when the caller's supplied default value was returned.
// Primary signal: absent variant key (flag-not-found, provider-not-ready, type-mismatch, no allocation).
// Secondary: explicit DEFAULT or DISABLED reason (belt-and-suspenders).
// Satisfies CONT-07 (source_comment_id 3395344504).
func isRuntimeDefault(details of.InterfaceEvaluationDetails) bool {
	if details.Variant == "" {
		return true
	}
	return details.Reason == of.DefaultReason || details.Reason == of.DisabledReason
}
