// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"

	of "github.com/open-feature/go-sdk/openfeature"
)

// evaluationTelemetryOptOutHook gates only Finally because the wrapped hooks
// emit telemetry only from Finally. The ordinary OpenFeature client supplies the
// final merged context, including changes returned by a failing Before hook.
// The experimental multi.Provider does not isolate that context; see doc.go.
type evaluationTelemetryOptOutHook struct {
	of.Hook
}

func withEvaluationTelemetryOptOut(hook of.Hook, enabled bool) of.Hook {
	if !enabled {
		return hook
	}
	return evaluationTelemetryOptOutHook{Hook: hook}
}

func (h evaluationTelemetryOptOutHook) Finally(ctx context.Context, hookContext of.HookContext, details of.InterfaceEvaluationDetails, hints of.HookHints) {
	optedOut, _ := hookContext.EvaluationContext().Attribute("dd_evaluation_telemetry_opt_out").(bool)
	if !optedOut {
		h.Hook.Finally(ctx, hookContext, details, hints)
	}
}
