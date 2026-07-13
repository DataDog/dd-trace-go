// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"strings"
	"testing"
)

func TestBuildDDTags(t *testing.T) {
	cfg := &config{
		service: "mysvc",
		env:     "prod",
		version: "v1.0",
	}

	tags := buildDDTags(cfg)

	wantContains := []string{
		"language:go",
		"go.version:",
		"library_version:",
		"service:mysvc",
		"env:prod",
		"version:v1.0",
	}
	for _, want := range wantContains {
		if !strings.Contains(tags, want) {
			t.Errorf("buildDDTags() = %q, want it to contain %q", tags, want)
		}
	}

	// Every element must be a well-formed "key:value" pair: a non-empty key
	// with no embedded colon, and a value.
	for pair := range strings.SplitSeq(tags, ",") {
		key, value, ok := strings.Cut(pair, ":")
		if !ok {
			t.Errorf("tag %q is not a key:value pair", pair)
			continue
		}
		if key == "" {
			t.Errorf("tag %q has an empty key", pair)
		}
		if strings.Contains(key, ":") {
			t.Errorf("tag %q key contains a raw colon", pair)
		}
		if value == "" {
			t.Errorf("tag %q has an empty value", pair)
		}
	}
}

func TestBuildDDTagsOmitsUnsetConfig(t *testing.T) {
	// With an empty config, the service/env/version tags must be absent, but
	// the always-present language/version tags must remain. Check on parsed
	// keys so that "go.version"/"library_version" don't false-match "version".
	tags := buildDDTags(&config{})

	if !strings.Contains(tags, "language:go") {
		t.Errorf("buildDDTags() = %q, want it to contain %q", tags, "language:go")
	}

	keys := make(map[string]bool)
	for pair := range strings.SplitSeq(tags, ",") {
		if key, _, ok := strings.Cut(pair, ":"); ok {
			keys[key] = true
		}
	}
	for _, absent := range []string{"service", "env", "version"} {
		if keys[absent] {
			t.Errorf("buildDDTags() = %q, want it to omit the %q tag for unset config", tags, absent)
		}
	}
}

func TestBuildDDTagsNilConfig(t *testing.T) {
	// A nil config must not panic and must still emit the base tags.
	tags := buildDDTags(nil)
	if !strings.Contains(tags, "language:go") {
		t.Errorf("buildDDTags(nil) = %q, want it to contain %q", tags, "language:go")
	}
}
