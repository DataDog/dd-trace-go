// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import "golang.org/x/mod/semver"

// Bounds of the trace-agent releases whose agent-side stats aggregation for
// the v1.0 trace protocol drops the `lang` dimension: addNowV1's aggregation
// key omits Lang while the v0.4 addNow sets it. v1 support (and the defect)
// arrived in 7.73.0; the fix, 2d6da48ff7b (#50485), was backported only to
// 7.79.x via cd8593bedcf (#50508), landing in 7.79.0-rc.6. It was never
// backported to 7.77.x or 7.78.x.
//
// Delete this file, agentOmitsLangInV1Stats and its call sites once 7.78 is
// out of support.
const (
	agentV1StatsLangFirst   = "v7.73.0"
	agentV1StatsLangFixedIn = "v7.79.0-rc.6"
)

// agentOmitsLangInV1Stats reports whether v identifies a trace-agent whose
// agent-side stats aggregation for the v1.0 trace protocol loses the `lang`
// dimension.
//
// It is deliberately fail-open: any version string that does not parse as
// semver reports false, i.e. "not affected". Other trace-agent
// implementations do not follow the Agent's versioning scheme, and an
// unldflagged build reports the "6.0.0" fallback — an empty string, "dev", or
// "datadogexporter-otelcol-0.155.0" must never be subjected to the
// workaround.
func agentOmitsLangInV1Stats(v string) bool {
	if v == "" {
		return false
	}
	if v[0] != 'v' {
		v = "v" + v // semver requires the leading "v"; the agent reports "7.77.0"
	}
	if !semver.IsValid(v) {
		return false
	}
	return semver.Compare(v, agentV1StatsLangFirst) >= 0 &&
		semver.Compare(v, agentV1StatsLangFixedIn) < 0
}
