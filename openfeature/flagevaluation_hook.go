// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package openfeature provides the EVP flag evaluation OpenFeature hook.
// This file contains signature-only stubs; the real implementation is in plan 02.
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
// Plan 02 implements the body; this is a signature-only stub.
func (h *flagEvaluationHook) Finally(
	ctx context.Context,
	hookContext of.HookContext,
	details of.InterfaceEvaluationDetails,
	_ of.HookHints,
) {
	panic("not implemented")
}

// isRuntimeDefault returns true when the caller's supplied default value was returned.
// Primary signal: absent variant key (flag-not-found, provider-not-ready, type-mismatch, no allocation).
// Secondary: explicit DEFAULT or DISABLED reason (belt-and-suspenders).
// Satisfies CONT-07 (source_comment_id 3395344504).
// Plan 02 implements the body; this is a signature-only stub.
func isRuntimeDefault(_ of.InterfaceEvaluationDetails) bool {
	panic("not implemented")
}
