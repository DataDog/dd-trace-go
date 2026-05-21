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
}

func TestScan_RealRepoTracerHasUnmigratedReads(t *testing.T) {
	// Smoke test: a top-level run over the real tracer should find DD_SITE
	// (used inside ddtrace/tracer/option.go).
	root := filepath.Join("..", "..")
	got, err := scan(root, defaultRecognizers(), defaultExcludes(root))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	sites := got["DD_SITE"]
	if len(sites) == 0 {
		t.Fatalf("expected DD_SITE call sites in real repo, got none")
	}
}
