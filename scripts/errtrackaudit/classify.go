// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import "strings"

const (
	classificationCandidate        = "CANDIDATE"
	classificationLikelyIneligible = "LIKELY_INELIGIBLE"
)

// ineligibleMarkers are the cheap textual signals that a log message surfaces
// the caller's environment or configuration rather than an SDK defect — the
// first exclusion of the adoption policy in internal/README.md ("When to
// report, and when not to"). Matching is case-insensitive on the constant
// format string.
//
// The classification is deliberately one-sided and conservative: a marker
// demotes a site to LIKELY_INELIGIBLE, and everything else stays CANDIDATE for
// a human to judge — including the positive signals the policy calls out, like
// "failed to marshal", "recovered panic", or "unexpected". A site marked
// LIKELY_INELIGIBLE by a false marker match only loses triage visibility, and
// a //errtrack:ignore directive is the documented way to say a review is done;
// but a heuristic that wrongly hid a real candidate would be worse, so the
// markers are limited to strongly user-facing-config-flavored words.
//
// "unknown" and "malformed" are deliberately NOT markers even though they
// often smell like user input: in this repository they also fire on SDK
// invariant failures the policy lists as reportable ("telemetry: unknown
// metric type", a malformed response we failed to parse), so demoting on
// them would hide real candidates. Those sites fall through to CANDIDATE and
// a human decides.
var ineligibleMarkers = []string{
	// Environment-variable and configuration names.
	"dd_", "otel_", "env var", "environment variable",
	// Rejected user-supplied values.
	"invalid", "unsupported", "deprecated", "unrecognized", "unparseable",
	// Missing or overridden configuration, and the resulting fallback behavior.
	"missing", "not set", "unset", "default", "fallback", "disabled",
	// Explicitly skipped user input.
	"ignoring", "ignored",
	// Caller-owned input.
	"user", "customer",
}

// classifyMessage tags one call site's constant message. It is a triage aid,
// never an eligibility verdict.
func classifyMessage(msg string) string {
	m := strings.ToLower(msg)
	for _, marker := range ineligibleMarkers {
		if strings.Contains(m, marker) {
			return classificationLikelyIneligible
		}
	}
	return classificationCandidate
}
