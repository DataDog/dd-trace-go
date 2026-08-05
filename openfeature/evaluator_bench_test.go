// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import "testing"

func BenchmarkCompareSemver(b *testing.B) {
	benchmarks := []struct {
		name       string
		left       string
		right      string
		wantBefore bool
	}{
		{name: "release", left: "1.2.3", right: "2.0.0", wantBefore: true},
		{name: "prerelease", left: "1.0.0-beta.2", right: "1.0.0-beta.11", wantBefore: true},
		{name: "build_metadata_ignored", left: "1.0.0+2", right: "1.0.0+11"},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			comparand, ok := parseSemver(benchmark.right)
			if !ok {
				b.Fatalf("parseSemver(%q) failed", benchmark.right)
			}
			b.ReportAllocs()

			var ordering int
			for b.Loop() {
				var version parsedSemver
				version, ok = parseSemver(benchmark.left)
				ordering = compareSemver(version, comparand)
			}
			if !ok || (ordering < 0) != benchmark.wantBefore {
				b.Fatalf("compareSemver(%q, %q) = (%d, %v)", benchmark.left, benchmark.right, ordering, ok)
			}
		})
	}
}

func BenchmarkEvaluateSemverCondition(b *testing.B) {
	comparand, ok := parseSemver("2.0.0-beta.11+build.11")
	if !ok {
		b.Fatal("parseSemver failed")
	}
	condition := &condition{
		Operator:        operatorSemverLT,
		Attribute:       "version",
		Value:           "2.0.0-beta.11+build.11",
		semverComparand: &comparand,
	}
	context := map[string]any{
		"version": "2.0.0-beta.2+build.2",
	}

	b.ReportAllocs()

	var matched bool
	for b.Loop() {
		matched = evaluateCondition(condition, context)
	}
	if !matched {
		b.Fatal("expected semantic version condition to match")
	}
}
