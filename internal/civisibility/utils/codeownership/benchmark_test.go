// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// Synthetic rules keep performance checks reproducible without publishing a
// customer's CODEOWNERS. Query positions exercise last-rule, first-rule and miss.
func benchmarkRules(dialect Dialect, count int) string {
	var text strings.Builder
	if dialect == GitLab {
		text.WriteString("[Services]\n")
	}
	for i := range count {
		fmt.Fprintf(&text, "/pkg/service%d/** @org/team%d @shared\n", i, i%20)
	}
	return text.String()
}

func BenchmarkParse(b *testing.B) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		for _, count := range []int{50, 2000} {
			b.Run(fmt.Sprintf("%d/%d", dialect, count), func(b *testing.B) {
				text := benchmarkRules(dialect, count)
				b.ReportAllocs()
				for b.Loop() {
					owners, err := Parse(strings.NewReader(text), dialect)
					if err != nil || owners.Diagnostics() != 0 {
						b.Fatal(err, owners.Diagnostics())
					}
				}
			})
		}
	}
}

func BenchmarkLookup(b *testing.B) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		c, err := Parse(strings.NewReader(benchmarkRules(dialect, 2000)), dialect)
		if err != nil {
			b.Fatal(err)
		}
		for _, name := range []string{"/pkg/service1999/test.go", "/pkg/service0/test.go", "/other/test.go"} {
			b.Run(fmt.Sprintf("%d%s", dialect, name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					c.Match(name)
				}
			})
		}
	}
}

func BenchmarkSectionUnion(b *testing.B) {
	for _, duplicate := range []bool{false, true} {
		var text strings.Builder
		for i := range 4 {
			owner := i
			if duplicate {
				owner = 0
			}
			fmt.Fprintf(&text, "[Section%d]\n* @team%d @shared\n", i, owner)
		}
		c, err := Parse(strings.NewReader(text.String()), GitLab)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(strconv.FormatBool(duplicate), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c.Match("/pkg/test.go")
			}
		})
	}
}
