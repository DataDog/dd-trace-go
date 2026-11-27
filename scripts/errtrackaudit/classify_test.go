// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import "testing"

func TestClassifyMessage(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		// Positive policy signals stay CANDIDATE by default — the heuristic
		// never promotes, it only demotes on user-facing signals.
		{"failed to marshal agent payload: %s", classificationCandidate},
		{"failed to encode span stats payload: %s", classificationCandidate},
		{"failed to decode agent response: %s", classificationCandidate},
		{"failed to parse remote config payload: %s", classificationCandidate},
		{"recovered panic in flush loop: %v", classificationCandidate},
		{"unexpected status code from intake: %d", classificationCandidate},
		// User-facing configuration and environment problems are the policy's
		// first exclusion.
		{"invalid regexp %q: %s", classificationLikelyIneligible},
		{"Invalid value for DD_LOGGING_RATE: %s", classificationLikelyIneligible},
		{"unsupported value, using default", classificationLikelyIneligible},
		{"deprecated option ignored: %s", classificationLikelyIneligible},
		{"malformed DD_TRACE_SAMPLING_RULES, ignoring: %s", classificationLikelyIneligible},
		{"missing API key", classificationLikelyIneligible},
		{"otel_exporter_otlp_endpoint not set, skipping", classificationLikelyIneligible},
		{"appsec disabled by configuration", classificationLikelyIneligible},
		{"error parsing user-supplied input: %s", classificationLikelyIneligible},
		{"env var DD_FOO is empty, falling back to %s", classificationLikelyIneligible},
		// "unknown" and "malformed" are deliberately not markers: they also
		// fire on SDK invariant failures (internal/telemetry/metrics.go's
		// "telemetry: unknown metric type", a malformed response we failed to
		// parse), which the policy lists as reportable. Real config messages
		// carrying those words usually carry another marker too
		// ("malformed DD_..." above is caught by "dd_"/"ignoring").
		{"telemetry: unknown metric type %q", classificationCandidate},
		{"malformed response from agent: %s", classificationCandidate},
		{"unknown option %s", classificationCandidate},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			if got := classifyMessage(tc.msg); got != tc.want {
				t.Errorf("classifyMessage(%q) = %s, want %s", tc.msg, got, tc.want)
			}
		})
	}
}

func TestClassifyMessage_EveryMarkerDemotes(t *testing.T) {
	for _, marker := range ineligibleMarkers {
		if got := classifyMessage("x " + marker + " y"); got != classificationLikelyIneligible {
			t.Errorf("marker %q did not demote the message (got %s)", marker, got)
		}
	}
}
