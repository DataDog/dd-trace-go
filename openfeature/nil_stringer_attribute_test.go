// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNilStringerAttributeDoesNotPanic(t *testing.T) {
	h := newOptOutHarness(t, ProviderConfig{ExperimentationOptOutMode: ExperimentationOptOutDisabled})
	var optOutAt *time.Time
	h.provider.configuration.ObserveFullEvaluationData = true
	result, err := h.client.BooleanValueDetails(context.Background(), "experiment", false,
		of.NewEvaluationContext("subject", map[string]any{experimentationOptOutAtAttribute: optOutAt}))
	require.NoError(t, err)
	require.True(t, result.Value)
	h.provider.exposureWriter.flush()
	h.provider.flagEvalLoggingWriter.drainAndFlush()
	require.Len(t, h.sink.exposures, 1)
	require.Len(t, h.sink.events, 1)
}

func TestStringerAttributesFlattening(t *testing.T) {
	var absent *time.Time
	present := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	attrs := map[string]any{"nil": absent, "present": &present, "value": present, "nested": map[string]any{"nil": absent, "present": &present}}
	want := map[string]any{"present": present.String(), "value": present.String(), "nested.present": present.String()}
	t.Run("exposure", func(t *testing.T) { assert.Equal(t, want, flattenContext(attrs)) })
	t.Run("evaluation logging", func(t *testing.T) {
		got, reasons := flattenAndPruneContext(attrs)
		assert.Equal(t, want, got)
		assert.Empty(t, reasons)
	})
}
