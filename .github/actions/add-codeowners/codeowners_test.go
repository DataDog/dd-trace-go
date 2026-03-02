// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAnnotate(t *testing.T) {
	input, err := os.ReadFile("input.xml")
	if err != nil {
		t.Fatalf("reading input: %s", err)
	}
	want, err := os.ReadFile("want.xml")
	if err != nil {
		t.Fatalf("reading expected output: %s", err)
	}
	got, err := annotate(input)
	if err != nil {
		t.Fatalf("annotating input JUnit XML: %s", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("did not get expected output. Difference:\n%s", diff)
	}
}
