// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"

	of "github.com/open-feature/go-sdk/openfeature"
)

// flagEvalLoggingHook implements the OpenFeature Hook interface to record EVP flagevaluation events.
// It uses the Finally hook stage (same as flagEvalMetricsHook) to cover success, error, and default paths.
// Finally fires on error/default paths, unlike After.
type flagEvalLoggingHook struct {
	of.UnimplementedHook
	writer *flagEvalLoggingWriter
}

// newFlagEvalLoggingHook creates a new EVP flag evaluation hook.
func newFlagEvalLoggingHook(w *flagEvalLoggingWriter) *flagEvalLoggingHook {
	return &flagEvalLoggingHook{writer: w}
}

// Finally is called after every flag evaluation (success or error).
// Using Finally (not After) ensures error-path and provider-not-ready evaluations are counted.
// Mirrors flageval_metrics.go's Finally stage; the EVP hook is a separate registered hook.
func (h *flagEvalLoggingHook) Finally(
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

// isRuntimeDefault returns true when this provider path returns the caller's supplied default.
// The primary signal is an absent variant key. Type mismatches are the exception: the provider
// may have produced a real variant, but the OpenFeature SDK returns the caller default after
// conversion fails.
func isRuntimeDefault(details of.InterfaceEvaluationDetails) bool {
	return details.Variant == "" || details.ErrorCode == of.TypeMismatchCode
}
