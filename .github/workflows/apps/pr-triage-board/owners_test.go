// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"slices"
	"testing"
)

const testCodeowners = `# comment line
/.github/                       @DataDog/dd-trace-go-guild
/appsec/                        @DataDog/asm-go
*appsec.go                      @DataDog/asm-go   # trailing comment
/contrib/                       @DataDog/apm-go @DataDog/apm-idm-go
/contrib/database/sql/          @DataDog/database-monitoring @DataDog/apm-go
/ddtrace/tracer/sqlcomment.go   @DataDog/database-monitoring
*go.mod                         @DataDog/apm-go
/docs/prefix*                   @alice
**/generated/                   @DataDog/generators
/vendor/                        @DataDog/vendors
!/vendor/keep/
/exact?.txt                     @bob
/a/**/leaf.go                   @DataDog/deep
*.md                            @DataDog/docs
`

func TestCodeownersOwnersOf(t *testing.T) {
	co := parseCodeowners(testCodeowners)
	tests := []struct {
		file string
		want []string
	}{
		{".github/workflows/ci.yml", []string{"DataDog/dd-trace-go-guild"}},
		{"appsec/waf.go", []string{"DataDog/asm-go"}},
		{"internal/appsec/appsec.go", []string{"DataDog/asm-go"}},
		{"internal/appsec/rules/rules.go", nil},
		{"contrib/net/http/http.go", []string{"DataDog/apm-go", "DataDog/apm-idm-go"}},

		// The last matching rule wins.
		{"contrib/database/sql/conn.go", []string{"DataDog/database-monitoring", "DataDog/apm-go"}},
		{"contrib/database/sql/go.mod", []string{"DataDog/apm-go"}},
		{"ddtrace/tracer/sqlcomment.go", []string{"DataDog/database-monitoring"}},

		// A directory pattern covers files below it but not a file of the same name.
		{"appsec", nil},
		{"appsecurity/x.go", nil},

		// Suffix patterns match at any depth.
		{"a/b/c/go.mod", []string{"DataDog/apm-go"}},
		{"x/foo-go.mod", []string{"DataDog/apm-go"}},
		{"x/go.mod.bak", nil},

		// Prefix pattern.
		{"docs/prefix.txt", []string{"alice"}},
		{"docs/prefix-dir/file.txt", []string{"alice"}},
		{"docs/other.txt", nil},

		// Unanchored directory pattern with a leading **/.
		{"generated/x.go", []string{"DataDog/generators"}},
		{"pkg/deep/generated/x.go", []string{"DataDog/generators"}},

		// Negation clears the owners of earlier matches.
		{"vendor/lib/lib.go", []string{"DataDog/vendors"}},
		{"vendor/keep/lib.go", nil},

		// Single character wildcard.
		{"exact1.txt", []string{"bob"}},
		{"exact12.txt", nil},
		{"exact/.txt", nil},

		{"a/b/c/leaf.go", []string{"DataDog/deep"}},
		{"README.md", []string{"DataDog/docs"}},
		{"unowned/file.go", nil},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			if got := co.ownersOf(tc.file); !slices.Equal(got, tc.want) {
				t.Errorf("ownersOf(%q) = %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestCodeownersNil(t *testing.T) {
	var co *codeowners
	if got := co.ownersOf("a.go"); got != nil {
		t.Errorf("ownersOf() on nil = %v, want nil", got)
	}
	if got := parseCodeowners("").ownersOfFiles([]string{"a.go"}, "x"); len(got) != 0 {
		t.Errorf("ownersOfFiles() on empty file = %v, want none", got)
	}
}

func TestCodeownersOwnersOfFiles(t *testing.T) {
	co := parseCodeowners(testCodeowners)
	tests := []struct {
		name    string
		files   []string
		exclude string
		want    []string
	}{
		{
			name:  "owners of more files come first",
			files: []string{"appsec/a.go", "contrib/net/http/h.go", "appsec/b.go", "appsec/c.go"},
			want:  []string{"DataDog/asm-go", "DataDog/apm-go", "DataDog/apm-idm-go"},
		},
		{
			name:  "ties keep first appearance",
			files: []string{"contrib/net/http/h.go", "appsec/a.go"},
			want:  []string{"DataDog/apm-go", "DataDog/apm-idm-go", "DataDog/asm-go"},
		},
		{
			name:    "the author is excluded case-insensitively",
			files:   []string{"docs/prefix.txt", "appsec/a.go"},
			exclude: "ALICE",
			want:    []string{"DataDog/asm-go"},
		},
		{name: "unowned files", files: []string{"unowned/file.go"}, want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := co.ownersOfFiles(tc.files, tc.exclude)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ownersOfFiles() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProposedOwners(t *testing.T) {
	owners := []string{"DataDog/asm-go", "bob"}
	tests := []struct {
		name    string
		current []string
		want    []string
	}{
		{"no current owner proposes all owners", nil, owners},
		{"unrelated current owner proposes all owners", []string{"carol"}, owners},
		{"current owner among the owners proposes nothing", []string{"carol", "BOB"}, nil},
		{"requested team among the owners proposes nothing", []string{"datadog/asm-go"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := proposedOwners(tc.current, owners); !slices.Equal(got, tc.want) {
				t.Errorf("proposedOwners() = %v, want %v", got, tc.want)
			}
		})
	}
	if got := proposedOwners([]string{"carol"}, nil); got != nil {
		t.Errorf("proposedOwners() without owners = %v, want nil", got)
	}
}

func TestNewItemOwners(t *testing.T) {
	co := parseCodeowners(testCodeowners)
	tests := []struct {
		name         string
		mutate       func(*PR)
		wantCurrent  []string
		wantProposed []string
	}{
		{
			name:         "nobody on the PR proposes the CODEOWNERS owners",
			mutate:       func(p *PR) { p.Files = []string{"appsec/waf.go"} },
			wantProposed: []string{"DataDog/asm-go"},
		},
		{
			name: "reviewer and assignee are current owners and the author is skipped",
			mutate: func(p *PR) {
				p.Files = []string{"appsec/waf.go"}
				p.Reviews = []Review{maintainerReview("bob", "COMMENTED", daysAgo(2)), maintainerReview("alice", "COMMENTED", daysAgo(2))}
				p.Assignees = []string{"bob", "carol"}
			},
			wantCurrent:  []string{"bob", "carol"},
			wantProposed: []string{"DataDog/asm-go"},
		},
		{
			name: "requested owner team suppresses the proposal",
			mutate: func(p *PR) {
				p.Files = []string{"appsec/waf.go"}
				p.RequestedReviewers = []string{"DataDog/asm-go"}
			},
			wantCurrent: []string{"DataDog/asm-go"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			it := newItem(newTestPR(tc.mutate), co)
			if !slices.Equal(it.Current, tc.wantCurrent) || !slices.Equal(it.Proposed, tc.wantProposed) {
				t.Errorf("current = %v, proposed = %v, want %v, %v", it.Current, it.Proposed, tc.wantCurrent, tc.wantProposed)
			}
		})
	}
}

func TestFilterByMaintainer(t *testing.T) {
	co := parseCodeowners(testCodeowners)
	byOwner := newItem(newTestPR(func(p *PR) { p.Number, p.Author = 1, "zed"; p.Files = []string{"docs/prefix.txt"} }), co)
	byReviewer := newItem(newTestPR(func(p *PR) {
		p.Number, p.Author = 2, "zed"
		p.Files = []string{"unowned/x.go"}
		p.RequestedReviewers = []string{"alice"}
	}), co)
	other := newItem(newTestPR(func(p *PR) { p.Number, p.Author = 3, "zed"; p.Files = []string{"appsec/waf.go"} }), co)

	got := filterByMaintainer([]Item{byOwner, byReviewer, other}, "@Alice")
	if len(got) != 2 || got[0].PR.Number != 1 || got[1].PR.Number != 2 {
		t.Errorf("filterByMaintainer() kept %d items, want PRs 1 and 2", len(got))
	}
}
