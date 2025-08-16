// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xlab/treeprint"
	"gotest.tools/v3/golden"
)

var testdata string

func init() {
	_, file, _, _ := runtime.Caller(0)
	testdata = filepath.Join(file, "..", "testdata")
}

func TestMatchesAny(t *testing.T) {
	cases, err := os.ReadDir(testdata)
	require.NoError(t, err)
	for _, caseDir := range cases {
		if !caseDir.IsDir() {
			continue
		}

		name := caseDir.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				expected *Trace
				actual   []*Trace
			)
			{
				data, err := os.ReadFile(filepath.Join(testdata, name, "expected.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &expected))
			}
			{
				data, err := os.ReadFile(filepath.Join(testdata, name, "actual.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &actual))
			}

			matches, diff := expected.matchesAny(actual, treeprint.NewWithRoot("Root"))
			goldFile := filepath.Join(name, "diff.txt")
			if matches != nil {
				golden.Assert(t, "<none>", goldFile)
				require.Empty(t, diff, 0)
			} else {
				require.NotEmpty(t, diff)
				golden.Assert(t, strings.TrimSpace(diff.String()), goldFile)
			}
		})
	}
}

func TestFromSimplified(t *testing.T) {
	testCases := []struct {
		name       string
		simplified string
		want       Traces
	}{
		{
			name:       "empty input",
			simplified: ``,
			want:       Traces{},
		},
		{
			name: "single span",
			simplified: `
[span.one]
`,
			want: Traces{
				{
					Tags: map[string]any{"name": "span.one"},
					Meta: map[string]string{},
				},
			},
		},
		{
			name: "nested spans",
			simplified: `
[root]
    [child.one]
        [grandchild]
    [child.two]
`,
			want: Traces{
				{
					Tags: map[string]any{"name": "root"},
					Meta: map[string]string{},
					Children: Traces{
						{
							Tags: map[string]any{"name": "child.one"},
							Meta: map[string]string{},
							Children: Traces{
								{
									Tags: map[string]any{"name": "grandchild"},
									Meta: map[string]string{},
								},
							},
						},
						{
							Tags: map[string]any{"name": "child.two"},
							Meta: map[string]string{},
						},
					},
				},
			},
		},
		{
			name: "multiple roots",
			simplified: `
[root1]
[root2]
    [child]
[root3]
`,
			want: Traces{
				{
					Tags: map[string]any{"name": "root1"},
					Meta: map[string]string{},
				},
				{
					Tags: map[string]any{"name": "root2"},
					Meta: map[string]string{},
					Children: Traces{
						{
							Tags: map[string]any{"name": "child"},
							Meta: map[string]string{},
						},
					},
				},
				{
					Tags: map[string]any{"name": "root3"},
					Meta: map[string]string{},
				},
			},
		},
		{
			name:       "tabs and spaces mixed",
			simplified: "[root]\n    [child1]\n\t[child2]\n\t    [grandchild]",
			want: Traces{
				{
					Tags: map[string]any{"name": "root"},
					Meta: map[string]string{},
					Children: Traces{
						{
							Tags: map[string]any{"name": "child1"},
							Meta: map[string]string{},
						},
						{
							Tags: map[string]any{"name": "child2"},
							Meta: map[string]string{},
							Children: Traces{
								{
									Tags: map[string]any{"name": "grandchild"},
									Meta: map[string]string{},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "with optional fields",
			simplified: `
[http.request | GET / | net/http | client]
    [http.request | GET / | net/http | server]
`,
			want: Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "client",
					},
					Children: Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /",
							},
							Meta: map[string]string{
								"component": "net/http",
								"span.kind": "server",
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := FromSimplified(tc.simplified)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestToSimplified(t *testing.T) {
	testCases := []struct {
		name   string
		traces Traces
		want   string
	}{
		{
			name:   "empty input",
			traces: Traces{},
			want:   "",
		},
		{
			name: "single span",
			traces: Traces{
				{
					Tags: map[string]any{"name": "span.one"},
					Meta: map[string]string{},
				},
			},
			want: `
[span.one]
`,
		},
		{
			name: "nested spans",
			traces: Traces{
				{
					Tags: map[string]any{"name": "root"},
					Meta: map[string]string{},
					Children: Traces{
						{
							Tags: map[string]any{"name": "child.one"},
							Meta: map[string]string{},
							Children: Traces{
								{
									Tags: map[string]any{"name": "grandchild"},
									Meta: map[string]string{},
								},
							},
						},
						{
							Tags: map[string]any{"name": "child.two"},
							Meta: map[string]string{},
						},
					},
				},
			},
			want: `
[root]
    [child.one]
        [grandchild]
    [child.two]
`,
		},
		{
			name: "multiple roots",
			traces: Traces{
				{
					Tags: map[string]any{"name": "root1"},
					Meta: map[string]string{},
				},
				{
					Tags: map[string]any{"name": "root2"},
					Meta: map[string]string{},
					Children: Traces{
						{
							Tags: map[string]any{"name": "child"},
							Meta: map[string]string{},
						},
					},
				},
			},
			want: `
[root1]
[root2]
    [child]
`,
		},
		{
			name: "tabs and spaces mixed",
			traces: Traces{
				{
					Tags: map[string]any{"name": "root"},
					Meta: map[string]string{},
					Children: Traces{
						{
							Tags: map[string]any{"name": "child1"},
							Meta: map[string]string{},
						},
						{
							Tags: map[string]any{"name": "child2"},
							Meta: map[string]string{},
							Children: Traces{
								{
									Tags: map[string]any{"name": "grandchild"},
									Meta: map[string]string{},
								},
							},
						},
					},
				},
			},
			want: `
[root]
    [child1]
    [child2]
        [grandchild]
`,
		},
		{
			name: "with optional fields",
			traces: Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /",
					},
					Meta: map[string]string{
						"component": "net/http",
						"span.kind": "client",
					},
					Children: Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "GET /",
							},
							Meta: map[string]string{
								"component": "net/http",
								"span.kind": "server",
							},
						},
					},
				},
			},
			want: `
[http.request | GET / | net/http | client]
    [http.request | GET / | net/http | server]
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToSimplified(tc.traces)
			assert.Equal(t, strings.ReplaceAll(strings.TrimPrefix(tc.want, "\n"), "\t", "    "), got)
		})
	}
}
