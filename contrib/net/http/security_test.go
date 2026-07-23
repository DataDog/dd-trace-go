// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// hasControlByte reports whether s contains a raw CR, LF, or NUL byte -- the
// bytes an attacker needs to smuggle extra header lines into a carrier that
// writes header values without validation.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n', 0x00:
			return true
		}
	}
	return false
}

// TestBaggageControlCharsNotInjectedOnOutboundHTTP reproduces the end-to-end
// impact of a poisoned baggage value: "v\r\nX-Evil:1" is exactly what an
// upstream "baggage: k=v%0D%0AX-Evil:1" header decodes to. Once that value is
// set as request baggage, the wrapped client's outbound call must not be
// rejected by net/http, and the downstream server must not see a raw CR/LF/
// NUL in the injected ot-baggage-* header.
func TestBaggageControlCharsNotInjectedOnOutboundHTTP(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")
	tracer.Start()
	defer tracer.Stop()

	var capturedHeaders http.Header
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport).(*roundTripper)

	ctx := baggage.Set(context.Background(), "k", "v\r\nX-Evil:1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req) //nolint:bodyclose
	require.NoError(t, err, "outbound call must not be rejected because of a poisoned ot-baggage-* header")
	defer resp.Body.Close()

	found := false
	for k, vals := range capturedHeaders {
		if !strings.HasPrefix(strings.ToLower(k), tracer.DefaultBaggageHeaderPrefix) {
			continue
		}
		found = true
		for _, v := range vals {
			assert.False(t, hasControlByte(v), "%s must not carry a raw control byte, got %q", k, v)
		}
	}
	assert.True(t, found, "expected an ot-baggage-* header on the outbound request")
}
