// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

func TestPropagatedLLMSpanCarrier(t *testing.T) {
	const hexID, decimalID = "6928d1a700000000abcdef0123456789", "139780883686467862853683758018840586121"
	for _, tt := range []struct {
		name          string
		tagParent     string
		baggageParent string
		wantCarrier   string
		wantParent    string
	}{
		{"nothing", "", "", "", ""},
		{"tags only", "111", "", llmobsCarrierTags, "111"},
		{"baggage only", "", "222", llmobsCarrierBaggage, "222"},
		{"both agree", "222", "222", llmobsCarrierBaggage, "222"},
		{"tags differ", "111", "222", llmobsCarrierStaleBaggage, "111"},
		{"root tag parent", "undefined", "222", llmobsCarrierBaggage, "222"},
		{"root baggage parent", "111", "undefined", llmobsCarrierTags, "111"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sc := &SpanContext{trace: newTrace()}
			if tt.tagParent != "" {
				sc.trace.setPropagatingTag(keyPropagatedLLMObsParentID, tt.tagParent)
				sc.trace.setPropagatingTag(keyPropagatedLLMObsTraceID, decimalID)
				sc.trace.setPropagatingTag(keyPropagatedLLMObsMLAPP, "from-tags")
			}
			if tt.baggageParent != "" {
				sc.setBaggageItem(baggageKeyLLMObsParentID, tt.baggageParent)
				sc.setBaggageItem(baggageKeyLLMObsTraceID, hexID)
				sc.setBaggageItem(baggageKeyLLMObsMLApp, "from-baggage")
			}

			p, carrier := propagatedLLMSpan(sc)
			assert.Equal(t, tt.wantCarrier, carrier)
			if tt.wantCarrier == "" {
				assert.Nil(t, p)
				return
			}
			require.NotNil(t, p)
			assert.Equal(t, tt.wantParent, p.SpanID)
			assert.Equal(t, hexID, p.TraceID, "both carriers give the canonical trace ID")
			if tt.wantCarrier == llmobsCarrierBaggage {
				assert.Equal(t, "from-baggage", p.MLApp)
			} else {
				assert.Equal(t, "from-tags", p.MLApp)
			}
		})
	}
}

func TestLLMObsBaggageInjectTextMapCarrier(t *testing.T) {
	// A plain map can hold the caller's header under "Baggage". The merged header
	// must replace it, not sit next to it.
	sc := &SpanContext{trace: newTrace()}
	sc.llmobsPropagated = &illmobs.PropagatedLLMSpan{SpanID: "555", TraceID: "6928d1a700000000abcdef0123456789"}
	carrier := TextMapCarrier{"Baggage": "tenant=acme"}

	require.NoError(t, (&propagatorBaggage{}).Inject(sc, carrier))

	assert.Equal(t, TextMapCarrier{
		"baggage": "llmobs.parent_id=555,llmobs.trace_id=6928d1a700000000abcdef0123456789,tenant=acme",
	}, carrier)
}
