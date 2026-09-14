// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"strings"
	"testing"
)

func TestSelectContribModules(t *testing.T) {
	tab, graph, root := testTable(t)

	tests := []struct {
		name    string
		files   []string
		wantAll bool
		// wantEmpty asserts no contrib module needs testing at all.
		wantEmpty bool
		want      []string
		notWant   []string
	}{
		{
			name:      "a leaf package no contrib imports selects nothing",
			files:     []string{"openfeature/provider.go"},
			wantEmpty: true,
		},
		{
			name:  "a contrib change selects that module",
			files: []string{"contrib/gin-gonic/gin/gin.go"},
			want:  []string{"contrib/gin-gonic/gin"},
			// namingschematest requires every contrib, so it is a legitimate
			// reverse dependency of any of them.
			notWant: []string{"contrib/net/http", "contrib/database/sql"},
		},
		{
			name:  "a widely required contrib pulls in its dependents",
			files: []string{"contrib/net/http/http.go"},
			want: []string{
				"contrib/net/http",
				"contrib/labstack/echo.v4",
				"contrib/gorilla/mux",
			},
			notWant: []string{"contrib/database/sql"},
		},
		{
			name:  "a leaf's measured dependents are honoured",
			files: []string{"llmobs/llmobs.go"},
			want: []string{
				"contrib/mark3labs/mcp-go",
				"contrib/modelcontextprotocol/go-sdk",
			},
			notWant: []string{"contrib/gin-gonic/gin"},
		},
		{
			name:    "core selects everything",
			files:   []string{"ddtrace/tracer/tracer.go"},
			wantAll: true,
		},
		{
			name: "contrib/os is root-module code, so it selects everything",
			// The upward go.mod walk finds no contrib module and escalates. A
			// "contrib/" prefix rule would have narrowed this to nothing.
			files:   []string{"contrib/os/os.go"},
			wantAll: true,
		},
		{
			name:    "an unclassified path selects everything",
			files:   []string{"zz-brand-new/thing.go"},
			wantAll: true,
		},
		{
			name:      "docs select nothing",
			files:     []string{"README.md"},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tab.classify(tt.files, graph)
			if err != nil {
				t.Fatalf("classify(%v) = %v", tt.files, err)
			}
			got, err := selectContribModules(root, res)
			if err != nil {
				t.Fatalf("selectContribModules(%v) = %v", tt.files, err)
			}

			switch {
			case tt.wantAll:
				if len(got) != 1 || got[0] != selectAll {
					t.Fatalf("selectContribModules(%v) = %v, want [%s]", tt.files, got, selectAll)
				}
				return
			case tt.wantEmpty:
				if len(got) != 0 {
					t.Fatalf("selectContribModules(%v) = %v, want no modules", tt.files, got)
				}
				return
			}

			if len(got) == 1 && got[0] == selectAll {
				t.Fatalf("selectContribModules(%v) = [%s], want a narrowed set", tt.files, selectAll)
			}
			for _, w := range tt.want {
				if !contains(got, w) {
					t.Errorf("selectContribModules(%v) is missing %q (got %v)", tt.files, w, got)
				}
			}
			for _, w := range tt.notWant {
				if contains(got, w) {
					t.Errorf("selectContribModules(%v) unexpectedly includes %q", tt.files, w)
				}
			}
		})
	}
}

// TestEnclosingModuleEscalatesForRootCode locks in the behaviour that makes
// contrib/os/ safe, independent of the table.
func TestEnclosingModule(t *testing.T) {
	_, _, root := testTable(t)
	_, byDir, err := loadModules(root)
	if err != nil {
		t.Fatalf("loadModules() = %v", err)
	}

	tests := []struct {
		file    string
		wantDir string
		wantSub bool
	}{
		{"contrib/gin-gonic/gin/gin.go", "contrib/gin-gonic/gin", true},
		{"contrib/gin-gonic/gin/internal/x/y.go", "contrib/gin-gonic/gin", true},
		{"contrib/cloud.google.com/go/pubsub.v1/option.go", "contrib/cloud.google.com/go/pubsub.v1", true},
		// No go.mod at any depth: root-module code wearing a contrib path.
		{"contrib/os/os.go", ".", false},
	}
	for _, tt := range tests {
		got, isSub := enclosingModule(byDir, tt.file)
		if got.Dir != tt.wantDir || isSub != tt.wantSub {
			t.Errorf("enclosingModule(%q) = (%q, %t), want (%q, %t)",
				tt.file, got.Dir, isSub, tt.wantDir, tt.wantSub)
		}
	}
}

// TestReverseDepsExcludesRootEdge guards the one thing that would make
// narrowing useless: every contrib requires the root module, so keeping that
// edge expands any seed to the whole matrix.
func TestReverseDepsExcludesRootEdge(t *testing.T) {
	_, _, root := testTable(t)
	byPath, _, err := loadModules(root)
	if err != nil {
		t.Fatalf("loadModules() = %v", err)
	}
	rev, err := reverseDeps(root, byPath)
	if err != nil {
		t.Fatalf("reverseDeps() = %v", err)
	}
	if dependents := rev["."]; len(dependents) != 0 {
		t.Errorf("reverseDeps()[%q] has %d dependents, want 0: the root-module edge must be "+
			"excluded or every seed expands to the full matrix", ".", len(dependents))
	}
}

// TestContribModulesCLIAlwaysEmitsASentinel is a regression test.
//
// The -contrib-modules output is consumed by a shell that defaults an unset
// value to ALL. Emitting nothing for "no module needs testing" made that
// indistinguishable from "no selection was provided", so the matrix quietly
// ran all ~72 modules for a change that needed none. Every verdict must print
// at least one line.
func TestContribModulesCLIAlwaysEmitsASentinel(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{"no contrib module needed", []string{"openfeature/provider.go"}, selectNone},
		{"docs need nothing", []string{"README.md"}, selectNone},
		{"core needs everything", []string{"ddtrace/tracer/tracer.go"}, selectAll},
		{"unclassified needs everything", []string{"zz-brand-new/thing.go"}, selectAll},
		{"empty change set needs everything", nil, selectAll},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			in := strings.NewReader(strings.Join(tt.files, "\n"))
			if err := run([]string{"-contrib-modules"}, in, &out); err != nil {
				t.Fatalf("run(-contrib-modules) = %v", err)
			}
			got := strings.Fields(out.String())
			if len(got) == 0 {
				t.Fatalf("run(-contrib-modules) printed nothing for %v; the consumer reads "+
					"an empty value as 'unset' and falls back to the full matrix", tt.files)
			}
			if got[0] != tt.want {
				t.Errorf("run(-contrib-modules) for %v = %q, want %q", tt.files, got[0], tt.want)
			}
		})
	}
}

// TestContribModulesNarrowedOutputHasNoSentinel keeps the two shapes disjoint:
// a narrowed list must not also carry ALL or NONE.
func TestContribModulesNarrowedOutputHasNoSentinel(t *testing.T) {
	var out strings.Builder
	in := strings.NewReader("contrib/gin-gonic/gin/gintrace.go")
	if err := run([]string{"-contrib-modules"}, in, &out); err != nil {
		t.Fatalf("run(-contrib-modules) = %v", err)
	}
	got := strings.Fields(out.String())
	if len(got) == 0 {
		t.Fatal("run(-contrib-modules) printed nothing for a contrib change")
	}
	for _, m := range got {
		if m == selectAll || m == selectNone {
			t.Errorf("narrowed output %v contains the sentinel %q", got, m)
		}
	}
}
