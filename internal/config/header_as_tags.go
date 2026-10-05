// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// parseHeaderAsTagsFromEnv reads DD_TRACE_HEADER_TAGS, splits it on commas, and
// returns the resulting list with the origin reported by the provider.
func parseHeaderAsTagsFromEnv(p *provider.Provider) ([]string, telemetry.Origin) {
	v, origin := p.GetStringWithOrigin("DD_TRACE_HEADER_TAGS", "")
	if v == "" {
		return nil, origin
	}
	return strings.Split(v, ","), origin
}

// propagateHeaderAsTagsToGlobalConfig is the apply callback for headerAsTags.
// Transitional: removed when integrations read header tags from this package directly.
func propagateHeaderAsTagsToGlobalConfig(headerAsTags []string) bool {
	globalconfig.ClearHeaderTags()
	for _, h := range headerAsTags {
		header, tag := normalizer.HeaderTag(h)
		if len(header) == 0 || len(tag) == 0 {
			log.Debug("Header-tag input is in unsupported format; dropping input value %q", h)
			continue
		}
		globalconfig.SetHeaderTag(header, tag)
	}
	return true
}
