// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"strings"
	"testing"
)

// newTestCompleteReport returns a Report with a non-incomplete crashed stack,
// as buildDDTags would see after a normal (non-truncated) parse.
func newTestCompleteReport() *Report {
	return &Report{
		Error: Error{
			Stack:   &StackTrace{Format: stackFormat, Incomplete: false},
			IsCrash: true,
		},
	}
}

func TestBuildDDTags(t *testing.T) {
	cfg := &config{
		service: "mysvc",
		env:     "prod",
		version: "v1.0",
	}

	tags := buildDDTags(cfg, newTestCompleteReport())

	wantContains := []string{
		"language_name:go",
		"language_version:",
		"tracer_version:",
		"service:mysvc",
		"env:prod",
		"version:v1.0",
		"data_schema_version:" + dataSchemaVersion,
		"incomplete:false",
		"is_crash:true",
	}
	for _, want := range wantContains {
		if !strings.Contains(tags, want) {
			t.Errorf("buildDDTags() = %q, want it to contain %q", tags, want)
		}
	}

	// uuid must be present and well-formed.
	if !strings.Contains(tags, "uuid:") {
		t.Errorf("buildDDTags() = %q, want a uuid tag", tags)
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
	// keys so that "language_version"/"tracer_version" don't false-match "version".
	tags := buildDDTags(&config{}, newTestCompleteReport())

	if !strings.Contains(tags, "language_name:go") {
		t.Errorf("buildDDTags() = %q, want it to contain %q", tags, "language_name:go")
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
	tags := buildDDTags(nil, newTestCompleteReport())
	if !strings.Contains(tags, "language_name:go") {
		t.Errorf("buildDDTags(nil) = %q, want it to contain %q", tags, "language_name:go")
	}
}

func TestBuildDDTagsOmitsZeroSignalFields(t *testing.T) {
	// A top-level "SIG…: …" form (e.g. SIGQUIT) has no code=/addr= to parse,
	// so SiCode/SiSigno stay at their zero value — which must be omitted from
	// ddtags exactly like the JSON view omits them (omitempty), not rendered
	// as a literal "0" that the JSON view disagrees with.
	r := newTestCompleteReport()
	r.SigInfo = &SigInfo{SiSignoHuman: "SIGQUIT"}
	tags := buildDDTags(&config{}, r)

	for _, absent := range []string{"si_code:", "si_signo:"} {
		if strings.Contains(tags, absent) {
			t.Errorf("buildDDTags() = %q, want it to omit %q for an unset signal field", tags, absent)
		}
	}
	if !strings.Contains(tags, "si_signo_human_readable:SIGQUIT") {
		t.Errorf("buildDDTags() = %q, want it to contain %q", tags, "si_signo_human_readable:SIGQUIT")
	}
}

func TestBuildDDTagsIncomplete(t *testing.T) {
	r := newTestCompleteReport()
	r.Error.Stack.Incomplete = true
	tags := buildDDTags(&config{}, r)
	if !strings.Contains(tags, "incomplete:true") {
		t.Errorf("buildDDTags() = %q, want it to contain %q", tags, "incomplete:true")
	}
}

func TestBuildDDTagsSignalTags(t *testing.T) {
	// SiCodeHuman is set here to test buildDDTags' own rendering in isolation;
	// parseSignal does not currently populate it (see
	// TestParseSignalDoesNotPopulateSiCodeHuman), so this value cannot yet
	// arise from a real parsed crash. si_code_human_readable is therefore
	// declared and rendered but never actually emitted with a value today.
	r := newTestCompleteReport()
	r.SigInfo = &SigInfo{
		SiAddr:       "0x0",
		SiCode:       1,
		SiCodeHuman:  "SEGV_MAPERR",
		SiSigno:      11,
		SiSignoHuman: "SIGSEGV",
	}
	tags := buildDDTags(&config{}, r)

	wantContains := []string{
		"si_addr:0x0",
		"si_code:1",
		"si_code_human_readable:SEGV_MAPERR",
		"si_signo:11",
		"si_signo_human_readable:SIGSEGV",
	}
	for _, want := range wantContains {
		if !strings.Contains(tags, want) {
			t.Errorf("buildDDTags() = %q, want it to contain %q", tags, want)
		}
	}
}
