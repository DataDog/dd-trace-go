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
// Finally fires on error/default paths, unlike After.
type flagEvaluationHook struct {
	of.UnimplementedHook
	writer *flagEvaluationWriter
}

// newFlagEvaluationHook creates a new EVP flag evaluation hook.
func newFlagEvaluationHook(w *flagEvaluationWriter) *flagEvaluationHook {
	return &flagEvaluationHook{writer: w}
}

// Finally is called after every flag evaluation (success or error).
// Using Finally (not After) ensures error-path and provider-not-ready evaluations are counted.
// Mirrors flageval_metrics.go's Finally stage; the EVP hook is a separate registered hook.
func (h *flagEvaluationHook) Finally(
	_ context.Context,
	hookContext of.HookContext,
	details of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	// Do NOT gate buffering on the evaluation context. In real servers ctx is
	// frequently the request context, which may already be cancelled by the time
	// Finally runs — and record() is a non-blocking in-memory add with no network
	// call. Gating on ctx.Done() would silently drop legitimate evaluation counts
	// for cancelled-request evals.
	if h.writer == nil {
		return
	}
	h.writer.record(hookContext, details)
}

// isRuntimeDefault returns true when the caller's supplied default value was returned.
// Signal: absent variant key. Our evaluator sets a variant ONLY on a matched allocation
// (reason TARGETING_MATCH/SPLIT/STATIC); every DEFAULT/DISABLED/ERROR path leaves the variant
// empty (see evaluator.go). A present variant therefore means a real assignment, not a default.
func isRuntimeDefault(details of.InterfaceEvaluationDetails) bool {
	return details.Variant == ""
}
