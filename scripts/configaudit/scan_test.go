// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestScan_Fixture(t *testing.T) {
	dir := filepath.Join("testdata", "fixture_a")
	// Recognizer matches by *unqualified* function name for the fixture, since
	// the fixture defines its own helpers. In the real codebase we match by
	// fully-qualified path.
	recog := recognizers{
		ByName: map[string]bool{
			"envGet":  true,
			"boolEnv": true,
			"intEnv":  true,
		},
	}
	got, err := scan(dir, recog, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	want := []string{"DD_HOSTNAME", "DD_PROFILING_ENABLED", "DD_SITE", "DD_TRACE_AGENT_PORT"}
	if len(gotKeys) != len(want) {
		t.Fatalf("got keys %v, want %v", gotKeys, want)
	}
	for i, k := range want {
		if gotKeys[i] != k {
			t.Errorf("got[%d]=%s, want %s", i, gotKeys[i], k)
		}
	}
	if len(got["DD_SITE"]) != 1 {
		t.Errorf("DD_SITE call-site count = %d, want 1", len(got["DD_SITE"]))
	}
	// DD_ENV is suppressed with //configaudit:ignore and must not appear.
	if len(got["DD_ENV"]) != 0 {
		t.Errorf("DD_ENV should be suppressed, got %d call sites", len(got["DD_ENV"]))
	}
}

func TestHasIgnoreDirective(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"bare", "//configaudit:ignore", true},
		{"with reason", "//configaudit:ignore — intentional direct read", true},
		{"spaced", "// configaudit:ignore", true},
		{"nolint form is not recognized", "//nolint:configaudit", false},
		{"unrelated directive", "//nolint:errcheck", false},
		{"plain comment", "// just a comment", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasIgnoreDirective(tc.text); got != tc.want {
				t.Errorf("hasIgnoreDirective(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestScan_RealRepoFindsUnmigratedReads(t *testing.T) {
	// Smoke test: DD_APPSEC_ENABLED is read directly in internal/appsec/config
	// and is outside the tracer migration scope, so it should always appear as
	// an unmigrated call site.
	root := filepath.Join("..", "..")
	got, err := scan(root, defaultRecognizers(), defaultExcludes())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got["DD_APPSEC_ENABLED"]) == 0 {
		t.Fatal("expected DD_APPSEC_ENABLED call sites in real repo, got none")
	}
}
