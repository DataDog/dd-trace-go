// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package log

// policyAction classifies what to do with a given log.Error format template.
type policyAction uint8

const (
	// policyReport forwards the log to telemetry as an error.
	policyReport policyAction = iota
	// policyDowngrade forwards the log to telemetry as a warning.
	policyDowngrade
	// policyExclude suppresses telemetry forwarding for the template.
	policyExclude
)

// policyTable maps constant log.Error format strings to their telemetry action.
//
// Seed rules:
//   - EXCLUDE: environmental and user-caused errors (agent connectivity, network
//     timeouts, user misconfiguration). These are expected operational noise and
//     should not appear in SDK Error Tracking.
//   - DOWNGRADE: low-severity internal notices that are not SDK defects.
//   - REPORT (default): all other templates are forwarded as errors.
//
// This is the single place to tune noise. Add or adjust entries here when a
// template produces unwanted events in Error Tracking.
var policyTable = map[string]policyAction{
	// ── Agent / network connectivity ────────────────────────────────────────
	// These reflect user environment issues, not SDK bugs.
	"failure sending traces (attempt %d of %d): %v":                                policyExclude,
	"lost %d traces: %v":                                                           policyExclude,
	"Error sending stats payload: %s":                                              policyExclude,
	"OTLP: failure sending traces (attempt %d of %d): %v":                          policyExclude,
	"OTLP: lost %d spans: %v":                                                      policyExclude,
	"logsWriter: failure sending logs data data: %s":                               policyExclude,
	"coverageWriter: failure sending coverage data: %s":                            policyExclude,
	"remoteconfig: http request error: could not read the response body: %s":       policyExclude,
	"remoteconfig: http request error: could not parse the json response body: %s": policyExclude,

	// ── W3C traceparent / context-propagation parse failures ────────────────
	// Malformed incoming headers are caused by upstream services, not this SDK.
	"failed to parse trace source tag: %s":           policyExclude,
	"failed to convert decision maker to uint32: %s": policyExclude,

	// ── Sampler-rule errors ──────────────────────────────────────────────────
	"Error marshalling SamplingRule to json: %s": policyExclude,

	// ── Telemetry pipeline self-reference ────────────────────────────────────
	// These originate from the telemetry writer's own payload-encoding path.
	// Forwarding them back through the same telemetry pipeline that just
	// failed to encode is circular and adds no signal: if the pipeline is
	// broken, the report likely won't arrive anyway; local log output already
	// captures it for debugging. Excluded to avoid a self-referential
	// report-about-the-reporter loop.
	"telemetry/writer: panic while encoding payload!":    policyExclude,
	"telemetry/writer: panic while encoding payload: %v": policyExclude,

	// ── User misconfiguration ────────────────────────────────────────────────
	"config: usage of a unlisted environment variable: %s": policyDowngrade,
}

// lookupPolicy returns the policyAction for the given format string.
// Unknown templates default to policyReport.
func lookupPolicy(format string) policyAction {
	if action, ok := policyTable[format]; ok {
		return action
	}
	return policyReport
}

// warnOptedIn reports whether a log.Warn format string has been explicitly
// opted in to telemetry forwarding. Unlike lookupPolicy, absence from the
// table means "not forwarded" — Warn is the noisy tier and defaults off.
func warnOptedIn(format string) bool {
	action, ok := policyTable[format]
	return ok && action == policyReport
}
