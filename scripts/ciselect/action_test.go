// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"reflect"
	"regexp"
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
	for _, extra := range []string{"run-all", "static-any", "changed-files"} {
		if !declared[extra] {
			t.Errorf("%s declares no %q output", actionPath, extra)
		}
	}
}
