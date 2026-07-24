// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

type preparedHeaderTag struct {
	header string
	tag    string
}

type preparedHeaderTags struct {
	tags     []preparedHeaderTag
	rejected []string
}

// prepareHeaderAsTagsForRegistry is a test seam for ordering an initial
// publication snapshot against a same-generation remote-config update.
var prepareHeaderAsTagsForRegistry = prepareHeaderAsTags

// parseHeaderAsTagsFromEnv reads DD_TRACE_HEADER_TAGS, splits it on commas, and
// returns the resulting list with the origin reported by the provider.
func parseHeaderAsTagsFromEnv(p *provider.Provider) ([]string, telemetry.Origin) {
	v, origin := p.GetStringWithOrigin("DD_TRACE_HEADER_TAGS", "")
	if v == "" {
		return nil, origin
	}
	return strings.Split(v, ","), origin
}

// prepareHeaderAsTags performs all parsing before the legacy registry is
// mutated. In particular, it does not log: logger callbacks may reenter config
// publication, so warnings are emitted only after the generation-fenced
// registry write completes.
func prepareHeaderAsTags(headerAsTags []string) preparedHeaderTags {
	prepared := preparedHeaderTags{
		tags: make([]preparedHeaderTag, 0, len(headerAsTags)),
	}
	for _, h := range headerAsTags {
		header, tag := normalizer.HeaderTag(h)
		if len(header) == 0 || len(tag) == 0 {
			prepared.rejected = append(prepared.rejected, h)
			continue
		}
		prepared.tags = append(prepared.tags, preparedHeaderTag{header: header, tag: tag})
	}
	return prepared
}

func preparedHeaderTagMap(prepared preparedHeaderTags) map[string]string {
	tags := make(map[string]string, len(prepared.tags))
	for _, tag := range prepared.tags {
		tags[tag.header] = tag.tag
	}
	return tags
}

func logRejectedHeaderTags(prepared preparedHeaderTags) {
	for _, rejected := range prepared.rejected {
		log.Debug("Header-tag input is in unsupported format; dropping input value %q", rejected)
	}
}
