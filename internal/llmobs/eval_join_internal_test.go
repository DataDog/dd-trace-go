// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// TestBuildEvaluationJoin locks the sentinel returned for each join input. The
// exact sentinel matters because SubmitEvaluation's telemetry buckets the error
// by identity (telemetryErrorTypes): a partially-specified family must report
// its own error even when the other family is also present, not "both present".
func TestBuildEvaluationJoin(t *testing.T) {
	cases := []struct {
		name                            string
		spanID, traceID, tagKey, tagVal string
		wantErr                         error
		wantSpan                        *transport.EvaluationSpanJoin
		wantTag                         *transport.EvaluationTagJoin
	}{
		{name: "valid span join", spanID: "s", traceID: "t", wantSpan: &transport.EvaluationSpanJoin{SpanID: "s", TraceID: "t"}},
		{name: "valid tag join", tagKey: "k", tagVal: "v", wantTag: &transport.EvaluationTagJoin{Key: "k", Value: "v"}},
		{name: "no join", wantErr: errEvalJoinNonePresent},
		{name: "partial span", spanID: "s", wantErr: errInvalidSpanJoin},
		{name: "partial tag", tagKey: "k", wantErr: errInvalidTagJoin},
		{name: "both full", spanID: "s", traceID: "t", tagKey: "k", tagVal: "v", wantErr: errEvalJoinBothPresent},
		// A partial family alongside the other family reports the partial family's
		// own sentinel (its telemetry error_type), not errEvalJoinBothPresent.
		{name: "partial span + full tag", spanID: "s", tagKey: "k", tagVal: "v", wantErr: errInvalidSpanJoin},
		{name: "full span + partial tag", spanID: "s", traceID: "t", tagKey: "k", wantErr: errInvalidTagJoin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joinOn, err := BuildEvaluationJoin(tc.spanID, tc.traceID, tc.tagKey, tc.tagVal)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSpan, joinOn.Span)
			assert.Equal(t, tc.wantTag, joinOn.Tag)
		})
	}
}
