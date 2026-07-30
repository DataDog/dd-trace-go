// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestBeforeRoundTripSkipsTracerVersionHeader(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	req, err := http.NewRequest("GET", "http://example.com", nil)
	require.NoError(t, err)
	req.Header.Set(tracerVersionHeader, "v2.0.0")

	ictx := hooktest.NewMockHookContext()
	BeforeRoundTrip(ictx, &http.Transport{}, req)

	assert.False(t, ictx.SkipCall)
	assert.Nil(t, ictx.GetData())
	assert.Empty(t, mt.FinishedSpans(), "no span should be started for the tracer's own requests")
}

func TestBeforeRoundTripInstrumentsNormalRequests(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	req, err := http.NewRequest("GET", "http://example.com", nil)
	require.NoError(t, err)

	ictx := hooktest.NewMockHookContext(&http.Transport{}, req)
	BeforeRoundTrip(ictx, &http.Transport{}, req)

	assert.False(t, ictx.SkipCall)
	_, ok := ictx.GetData().(wrap.AfterRoundTrip)
	require.True(t, ok, "BeforeRoundTrip must stash the after-hook for a normal request")
	assert.NotEqual(t, req, ictx.GetParam(1), "the request param must be replaced with the traced clone")
}

func TestRoundTripRASPBlockSkipsRealCall(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../../internal/appsec/testdata/rasp.json")
	t.Setenv("DD_APPSEC_RASP_ENABLED", "true")

	mt := mocktracer.Start()
	defer mt.Stop()

	testutils.StartAppSec(t)

	// RASP only inspects outgoing requests that carry a monitored inbound
	// handler operation in their context, so set one up the same way a traced
	// net/http server would. The SSRF detector correlates the outbound URL
	// against tainted inbound request data, so the query value must match the
	// outbound target verbatim.
	incoming, err := http.NewRequest("GET", "http://localhost/?value=169.254.169.254", nil)
	require.NoError(t, err)
	_, tracedReq, afterHandle, _ := httptrace.BeforeHandle(&httptrace.ServeConfig{
		Service:  "service",
		Resource: "resource",
	}, httptest.NewRecorder(), incoming)
	defer afterHandle()

	// 169.254.169.254 is blocked by the RASP SSRF rule in rasp.json.
	req, err := http.NewRequestWithContext(tracedReq.Context(), "GET", "http://169.254.169.254", nil)
	require.NoError(t, err)

	ictx := hooktest.NewMockHookContext()
	BeforeRoundTrip(ictx, &http.Transport{}, req)

	require.True(t, ictx.SkipCall, "a RASP block must skip the real RoundTrip call, not let it through")
	blockErr, ok := ictx.GetData().(error)
	require.True(t, ok, "BeforeRoundTrip must stash the blocking error for AfterRoundTrip")
	assert.True(t, events.IsSecurityError(blockErr))

	// Simulate what the generated trampoline does when SetSkipCall(true): it
	// calls AfterRoundTrip with the (zero-value, never populated) return slots
	// before returning them to the RoundTrip caller.
	AfterRoundTrip(ictx, nil, nil)

	require.Len(t, ictx.ReturnVals, 2)
	assert.Nil(t, ictx.ReturnVals[0])
	assert.Equal(t, blockErr, ictx.ReturnVals[1])
}
