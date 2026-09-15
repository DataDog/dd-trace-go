// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const reportCodeowners = `/internal/    @DataDog/guild
/ddtrace/     @DataDog/apm-go
`

var reportSites = []Site{
	{File: "internal/env/config.go", Line: 30, Package: "internal/env", Func: "load", Level: levelError, Message: "reviewed site", Ignored: true},
	{File: "ddtrace/tracer/writer.go", Line: 10, Package: "ddtrace/tracer", Func: "flush", Level: levelError, Message: "failed to flush traces: %s"},
	{File: "internal/env/config.go", Line: 20, Package: "internal/env", Func: "load", Level: levelWarn, Message: "invalid DD_FOO: %s"},
}

func buildTestReport(t *testing.T) Report {
	t.Helper()
	co := mustLoadCodeowners(t, reportCodeowners)
	return buildReport(reportSites, co)
}

func TestBuildReport_GroupsSortAndTotals(t *testing.T) {
	rep := buildTestReport(t)
	if len(rep.Owners) != 2 {
		t.Fatalf("got %d owner groups, want 2", len(rep.Owners))
	}
	// Owner groups sort by name.
	if rep.Owners[0].Owner != "@DataDog/apm-go" || rep.Owners[1].Owner != "@DataDog/guild" {
		t.Errorf("owners not sorted by name: %q, %q", rep.Owners[0].Owner, rep.Owners[1].Owner)
	}
	if got, want := rep.Summary, (Totals{Sites: 3, Candidate: 1, LikelyIneligible: 1, Ignored: 1}); got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
	apm := rep.Owners[0]
	if apm.Totals != (Totals{Sites: 1, Candidate: 1}) {
		t.Errorf("apm-go totals = %+v", apm.Totals)
	}
	guild := rep.Owners[1]
	if guild.Totals != (Totals{Sites: 2, LikelyIneligible: 1, Ignored: 1}) {
		t.Errorf("guild totals = %+v", guild.Totals)
	}
	// Ignored sites drop out of the site list; the rest sort by file and line.
	if len(guild.Sites) != 1 || guild.Sites[0].Classification != classificationLikelyIneligible {
		t.Errorf("guild sites = %+v", guild.Sites)
	}
	if apm.Sites[0].Classification != classificationCandidate {
		t.Errorf("apm-go classification = %s, want %s", apm.Sites[0].Classification, classificationCandidate)
	}
}

func TestBuildReport_UnownedGroup(t *testing.T) {
	co := mustLoadCodeowners(t, "/internal/    @DataDog/guild\n")
	rep := buildReport([]Site{
		{File: "orphan/thing.go", Line: 1, Package: "orphan", Func: "f", Level: levelError, Message: "kept"},
	}, co)
	if len(rep.Owners) != 1 || rep.Owners[0].Owner != unownedPlaceholder {
		t.Fatalf("owners = %+v, want one %q group", rep.Owners, unownedPlaceholder)
	}
}

func TestRenderTable_Golden(t *testing.T) {
	rep := buildTestReport(t)
	var buf bytes.Buffer
	if err := renderTable(&buf, rep); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	want := `OWNER: @DataDog/apm-go — 1 sites (1 CANDIDATE, 0 LIKELY_INELIGIBLE, 0 ignored)
  FILE                      LINE  LEVEL  CLASSIFICATION  MESSAGE
  ddtrace/tracer/writer.go  10    ERROR  CANDIDATE       failed to flush traces: %s

OWNER: @DataDog/guild — 2 sites (0 CANDIDATE, 1 LIKELY_INELIGIBLE, 1 ignored)
  FILE                    LINE  LEVEL  CLASSIFICATION     MESSAGE
  internal/env/config.go  20    WARN   LIKELY_INELIGIBLE  invalid DD_FOO: %s

SUMMARY: 3 sites (1 CANDIDATE, 1 LIKELY_INELIGIBLE, 1 ignored) across 2 owners
`
	if got := buf.String(); got != want {
		t.Errorf("renderTable output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderTable_DeterministicAcrossRuns(t *testing.T) {
	rep := buildTestReport(t)
	var a, b bytes.Buffer
	if err := renderTable(&a, rep); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	if err := renderTable(&b, rep); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	if a.String() != b.String() {
		t.Error("renderTable is not deterministic")
	}
}

func TestRenderJSON_RoundTripAndOrdering(t *testing.T) {
	rep := buildTestReport(t)
	var a, b bytes.Buffer
	if err := renderJSON(&a, rep); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	if err := renderJSON(&b, rep); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	if a.String() != b.String() {
		t.Error("renderJSON is not deterministic")
	}
	var back Report
	if err := json.Unmarshal(a.Bytes(), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(rep, back) {
		t.Errorf("JSON round-trip mismatch:\n%+v\n%+v", rep, back)
	}
	if !strings.Contains(a.String(), `"likely_ineligible"`) {
		t.Error("JSON should use the documented snake_case keys")
	}
}

func TestFilterByPackage(t *testing.T) {
	sites := []Site{
		{File: "a.go", Line: 1, Package: "ddtrace/tracer", Func: "f", Level: levelError, Message: "kept"},
		{File: "b.go", Line: 2, Package: "ddtrace/opentelemetry", Func: "g", Level: levelWarn, Message: "kept"},
		{File: "c.go", Line: 3, Package: "internal/env", Func: "h", Level: levelError, Message: "dropped"},
	}
	if got := filterByPackage(sites, ""); len(got) != 3 {
		t.Errorf("empty prefix: got %d sites, want 3", len(got))
	}
	if got := filterByPackage(sites, "ddtrace/tracer"); len(got) != 1 || got[0].File != "a.go" {
		t.Errorf("ddtrace/tracer prefix: got %+v", got)
	}
	if got := filterByPackage(sites, "ddtrace"); len(got) != 2 {
		t.Errorf("ddtrace prefix: got %d sites, want 2", len(got))
	}
	if got := filterByPackage(sites, "nope"); len(got) != 0 {
		t.Errorf("nope prefix: got %d sites, want 0", len(got))
	}
}

func TestRun_UnknownFormat(t *testing.T) {
	root := filepath.Join("testdata", "fixture")
	var out bytes.Buffer
	err := run(root, "yaml", "", &out)
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("run with unknown format error = %v, want unknown format error", err)
	}
}
