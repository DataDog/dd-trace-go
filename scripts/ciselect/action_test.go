// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const actionPath = ".github/actions/changed-components/action.yml"

// The fallback loop in the action is a plain shell list, because it has to run
// when `go run ./scripts/ciselect` is what failed. That makes it the one place
// the gate list is duplicated, so it gets asserted rather than trusted.
var (
	fallbackGate = regexp.MustCompile(`(?m)^\s{12}([a-z-]+) \\$|^\s{12}([a-z-]+); do$`)
	actionOutput = regexp.MustCompile(`(?m)^  ([a-z-]+):\n    description:`)
)

func TestActionFallbackListMatchesGates(t *testing.T) {
	tab, _, root := testTable(t)

	body := readText(t, root, actionPath)

	var got []string
	for _, m := range fallbackGate.FindAllStringSubmatch(body, -1) {
		if m[1] != "" {
			got = append(got, m[1])
		} else {
			got = append(got, m[2])
		}
	}
	if len(got) == 0 {
		t.Fatalf("%s: found no fallback gate list; did the shell loop change shape?", actionPath)
	}

	want := tab.allGates()
	if !reflect.DeepEqual(sorted(got), want) {
		t.Errorf("%s fallback list does not match `gates:` in %s\n got: %v\nwant: %v",
			actionPath, tableRelPath, sorted(got), want)
	}
}

func TestActionDeclaresAnOutputPerGate(t *testing.T) {
	tab, _, root := testTable(t)

	body := readText(t, root, actionPath)
	declared := map[string]bool{}
	for _, m := range actionOutput.FindAllStringSubmatch(body, -1) {
		declared[m[1]] = true
	}
	for _, gate := range tab.Gates {
		if !declared[gate] {
			t.Errorf("%s declares no output for gate %q", actionPath, gate)
		}
	}
	for _, extra := range []string{"run-all", "static-any", "changed-files", "contrib-modules", "otelc-suites", "orchestrion-suites"} {
		if !declared[extra] {
			t.Errorf("%s declares no %q output", actionPath, extra)
		}
	}
}

// TestActionFailOpenEmitsSuiteTokens pins the fail-open branch: a ciselect
// failure runs the full suite matrices, so the suite outputs must carry the
// same ALL token the gates list does. Events other than pull_request do not
// take this branch: they reach ciselect with an empty change set and get ALL
// through RunAll.
func TestActionFailOpenEmitsSuiteTokens(t *testing.T) {
	_, _, root := testTable(t)

	body := readText(t, root, actionPath)
	// strings.Contains scans the whole file, so a stray token in the success
	// path would keep this green; slice from the else branch.
	elseBody := body[strings.Index(body, "\n        else\n"):]
	for _, token := range []string{"contrib-modules=ALL", "otelc-suites=ALL", "orchestrion-suites=ALL"} {
		if !strings.Contains(elseBody, token) {
			t.Errorf("%s fail-open branch does not emit %s", actionPath, token)
		}
	}
}

// TestActionOutputsPointAtTheClassifyStep pins the wiring: every declared
// output must read its value from the classify step, so a renamed step id
// cannot silently detach an output. changed-files is the one exception: it
// exposes the diff step's file list for reuse in the same job.
func TestActionOutputsPointAtTheClassifyStep(t *testing.T) {
	_, _, root := testTable(t)

	body := readText(t, root, actionPath)
	for _, m := range actionOutput.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if name == "changed-files" {
			continue
		}
		want := "value: ${{ steps.classify.outputs." + name + " }}"
		if !strings.Contains(body, want) {
			t.Errorf("%s output %q is not wired to steps.classify.outputs.%s", actionPath, name, name)
		}
	}
}
