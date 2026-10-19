// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func ExampleParse() {
	rules, err := Parse(strings.NewReader("*.go @go-team\n/docs/ @docs-team"), GitHub)
	if err != nil {
		panic(err)
	}
	owners, _ := rules.Match("pkg/server/server_test.go")
	fmt.Println(owners.FirstOwner())
	fmt.Println(owners.Tag())
	// Output:
	// @go-team
	// ["@go-team"]
}

func TestRepositoryExamples(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect Dialect
		files   []fileTest
	}{
		{"github", GitHub, []fileTest{
			{"go.mod", []string{"@go-team"}},
			{"pkg/api/server_test.go", []string{"@api-team"}},
			{"pkg/api/internal/auth.go", []string{"@security-team", "@api-team"}},
			{"docs/setup.md", []string{"@docs-team", "docs@example.com"}},
			{"generated/client.go", nil},
			{"README.md", []string{"@platform"}},
		}},
		{"gitlab", GitLab, []fileTest{
			{"pkg/api/server_test.go", []string{"@platform", "@api-team"}},
			{"pkg/api/internal/auth.go", []string{"@platform", "@api-team", "@security-team", "@@maintainers"}},
			{"docs/setup.md", []string{"@platform", "@docs-team"}},
			{"generated/client.go", []string{"@go-team"}},
			{"README.md", []string{"@platform"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := Load(filepath.Join("testdata", tc.name+".CODEOWNERS"), tc.dialect)
			if err != nil {
				t.Fatal(err)
			}
			if rules.Diagnostics() != 0 {
				t.Fatal(rules.Diagnostics())
			}
			for _, file := range tc.files {
				owners, _ := rules.Match(file.path)
				if !slices.Equal(owners.Owners(), file.owners) {
					t.Errorf("path=%q owners=%v want=%v", file.path, owners.Owners(), file.owners)
				}
			}
		})
	}
}

func TestUnicodePaths(t *testing.T) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		checkRules(t, dialect, []ruleTest{
			{name: "question mark is one rune", rules: "?.go @one", files: []fileTest{{"😀.go", []string{"@one"}}, {"𐐀.go", []string{"@one"}}, {"ab.go", nil}, {"x/a.go", []string{"@one"}}}},
			{name: "two question marks need two runes", rules: "??.go @two", files: []fileTest{{"😀.go", nil}, {"é😀.go", []string{"@two"}}}},
			{name: "literal rune", rules: "é😀.go @unicode", files: []fileTest{{"é😀.go", []string{"@unicode"}}, {"É😀.go", nil}}},
			{name: "escaped rune", rules: "\\😀.go @unicode", files: []fileTest{{"😀.go", []string{"@unicode"}}}},
			{name: "star retries at rune boundaries", rules: "*😀?.go @unicode", files: []fileTest{{"é😀𐐀.go", []string{"@unicode"}}, {"é😀.go", nil}}},
		})
	}
	checkRules(t, GitLab, []ruleTest{
		{name: "supplementary character class", rules: "/[😀].go @emoji", files: []fileTest{{"😀.go", []string{"@emoji"}}, {"é.go", nil}}},
		{name: "Unicode range", rules: "/[α-ω].go @greek", files: []fileTest{{"λ.go", []string{"@greek"}}, {"Λ.go", nil}}},
		{name: "negated Unicode range", rules: "/[!α-ω].go @other", files: []fileTest{{"😀.go", []string{"@other"}}, {"λ.go", nil}}},
		{name: "combining mark is a separate rune", rules: "??.go @two", files: []fileTest{{"e\u0301.go", []string{"@two"}}, {"é.go", nil}}},
	})
}

func TestGoUnicodeSectionsAndOwners(t *testing.T) {
	for _, names := range [][2]string{{"Σ", "ς"}, {"s", "ſ"}, {"ı", "I"}, {"𐐀", "𐐨"}} {
		rules, err := Parse(strings.NewReader("["+names[0]+"]\n*.go @first\n["+names[1]+"]\n*.go @second"), GitLab)
		if err != nil {
			t.Fatal(err)
		}
		owners, _ := rules.Match("main.go")
		if owners.Tag() != `["@second"]` || len(rules.sections) != 2 {
			t.Errorf("sections=%q owners=%s", names, owners.Tag())
		}
	}
	rules, err := Parse(strings.NewReader("[Docs] docs@𐐀.example\n*.go"), GitLab)
	if err != nil {
		t.Fatal(err)
	}
	owners, _ := rules.Match("main.go")
	if owners.FirstOwner() != "docs@𐐀.example" || rules.Diagnostics() != 0 {
		t.Fatal(owners.Tag(), rules.Diagnostics())
	}
	// Approval counts must be decimal integers even when Go recognizes the
	// characters as Unicode digits. An invalid count is diagnosed once.
	for _, number := range []string{"𝟙", "𞓰", "١"} {
		rules, err := Parse(strings.NewReader("[Docs]["+number+"] @team\n*.go"), GitLab)
		if err != nil {
			t.Fatal(err)
		}
		owners, _ := rules.Match("main.go")
		if owners.FirstOwner() != "@team" || rules.Diagnostics() != 1 {
			t.Fatal(number, owners.Tag(), rules.Diagnostics())
		}
	}
}

func TestPatternLengthCountsRunes(t *testing.T) {
	for _, dialect := range []Dialect{GitHub, GitLab} {
		for _, size := range []int{maximumPatternLength, maximumPatternLength + 1} {
			pattern := strings.Repeat("😀", size)
			rules, err := Parse(strings.NewReader("* @fallback\n/"+pattern+" @emoji"), dialect)
			if err != nil {
				t.Fatal(err)
			}
			owners, _ := rules.Match(pattern)
			want, diagnostics := "@emoji", 0
			if size > maximumPatternLength {
				want, diagnostics = "@fallback", 1
			}
			if owners.FirstOwner() != want || rules.Diagnostics() != diagnostics {
				t.Errorf("dialect=%d runes=%d owners=%v diagnostics=%d", dialect, size, owners.Owners(), rules.Diagnostics())
			}
		}
	}
}
