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
	"failure sending traces (attempt %d of %d): %v":          policyExclude,
	"lost %d traces: %v":                                      policyExclude,
	"Error sending stats payload: %s":                         policyExclude,
	"OTLP: failure sending traces (attempt %d of %d): %v":    policyExclude,
	"OTLP: lost %d spans: %v":                                 policyExclude,
	"logsWriter: failure sending logs data data: %s":          policyExclude,
	"coverageWriter: failure sending coverage data: %s":       policyExclude,
	"remoteconfig: http request error: could not read the response body: %s":        policyExclude,
	"remoteconfig: http request error: could not parse the json response body: %s":  policyExclude,

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
