// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// parseGlobalTagsFromEnv reads DD_TAGS and returns the parsed tags with the
// origin reported by the provider. OTEL_RESOURCE_ATTRIBUTES is handled
// transparently: the provider remaps it onto DD_TAGS (see
// provider/otelenvconfigsource.go), so reading DD_TAGS picks up both sources in
// the correct precedence order. Git-metadata tags are stripped, matching the
// legacy tracer behavior, so they don't leak onto every span.
func parseGlobalTagsFromEnv(p *provider.Provider) (map[string]any, telemetry.Origin) {
	v, origin := p.GetStringWithOrigin("DD_TAGS", "")
	if v == "" {
		return nil, origin
	}
	parsed := internal.ParseTagString(v)
	internal.CleanGitMetadataTags(parsed)
	if len(parsed) == 0 {
		return nil, origin
	}
	tags := make(map[string]any, len(parsed))
	for k, val := range parsed {
		tags[k] = val
	}
	return tags, origin
}
