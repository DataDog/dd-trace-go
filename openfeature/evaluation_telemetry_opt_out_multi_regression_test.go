// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/multi"
	"github.com/stretchr/testify/require"
)

// This is a tripwire for a known upstream v1.18.0 multi.Provider defect, not a
// correctness assertion. A nested call replaces the shared capturedContext,
// so the outer opt-out is lost. If an SDK upgrade changes the observed counts,
// check whether isolation is fixed. Correct counts are (1, 2, 2); then update
// this test and the multi.Provider caveat in doc.go.
func TestEvaluationTelemetryOptOutMultiProviderKnownContextLeak(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{EnableEvaluationTelemetryOptOutParameter: new(true)})
	hook := optOutTestHook{after: func(_ context.Context, hc of.HookContext) error {
		if hc.EvaluationContext().TargetingKey() == "outer" {
			h.evaluate(t, "inner", map[string]any{optOutTestParameter: false})
		}
		return nil
	}}
	p, err := multi.NewProvider(multi.StrategyFirstMatch, multi.WithProvider("datadog", h.provider, hook))
	require.NoError(t, err)
	t.Cleanup(p.Shutdown)
	require.NoError(t, of.SetNamedProviderAndWait(t.Name()+"/multi", p))
	h.client = of.NewClient(t.Name() + "/multi")
	h.evaluate(t, "outer", map[string]any{optOutTestParameter: true})
	h.assertCounts(t, 2, 2, 2)
}
