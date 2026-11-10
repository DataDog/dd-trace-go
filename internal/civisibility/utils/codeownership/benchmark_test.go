// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
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

// Load covers the cold path: the size check, the file read and, for UTF-16,
// the decode before parsing.
func BenchmarkLoad(b *testing.B) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		text := benchmarkRules(dialect, 2000)
		units := append([]uint16{0xfeff}, utf16.Encode([]rune(text))...)
		utf16Data := make([]byte, len(units)*2)
		for i, unit := range units {
			binary.LittleEndian.PutUint16(utf16Data[i*2:], unit)
		}
		for _, encoding := range []struct {
			name string
			data []byte
		}{{"utf8", []byte(text)}, {"utf16", utf16Data}} {
			b.Run(fmt.Sprintf("%d/%s", dialect, encoding.name), func(b *testing.B) {
				filename := filepath.Join(b.TempDir(), "CODEOWNERS")
				if err := os.WriteFile(filename, encoding.data, 0600); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					owners, err := Load(filename, dialect)
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

// Tag is built once per ownership: a typical list, and one far beyond the
// tag length, which must be cut while encoding.
func BenchmarkTag(b *testing.B) {
	for _, count := range []int{3, 100_000} {
		owners := make([]string, count)
		for i := range owners {
			owners[i] = fmt.Sprintf("@org/team%06d", i)
		}
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if newOwnership(owners).Tag() == "" {
					b.Fatal("empty tag")
				}
			}
		})
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
