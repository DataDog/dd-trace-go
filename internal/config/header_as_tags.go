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
// returns the resulting list with the origin (OriginEnvVar when set, OriginDefault otherwise).
func parseHeaderAsTagsFromEnv(p *provider.Provider) ([]string, telemetry.Origin) {
	v := p.GetString("DD_TRACE_HEADER_TAGS", "")
	if v == "" {
		return nil, telemetry.OriginDefault
	}
	return strings.Split(v, ","), telemetry.OriginEnvVar
}

// propagateHeaderAsTagsToGlobalConfig replaces globalconfig.HeaderTags with the
// parsed mapping derived from headerAsTags. Invoked as the apply callback on the
// headerAsTags DynamicConfig so the denormalized view stays in sync with every
// update (env load, programmatic setter, RC).
//
// Transitional: this lives in internal/config only until the integrations that
// read globalconfig.HeaderTags migrate to reading from this package directly.
// At that point this function, the globalconfig import, and the normalizer
// import all go away together.
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
