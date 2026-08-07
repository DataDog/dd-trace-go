// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package apimcallout

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	groovyGetAttribute = regexp.MustCompile(`getAttribute\(\s*'([^']+)'\s*\)`)
	jsonPathExpr       = regexp.MustCompile(`jsonPath\([^,]+,\s*'([^']+)'\)`)
)

// TestBoomiTemplateAttributeConsistency guards against the class of bug where a
// Boomi block enforcer reads a context attribute that the callout template never
// sets, which silently disables AppSec blocking. It needs no Groovy runtime: it
// statically asserts every attribute read by each *-block.groovy is produced by
// the matching *-callout.json variable extraction.
func TestBoomiTemplateAttributeConsistency(t *testing.T) {
	cases := []struct {
		name        string
		calloutFile string
		groovyFile  string
	}{
		{"request", "deploy/boomi/boomi-request-callout.json", "deploy/boomi/boomi-request-block.groovy"},
		{"response", "deploy/boomi/boomi-response-callout.json", "deploy/boomi/boomi-response-block.groovy"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := calloutVariableNames(t, tc.calloutFile)
			read := groovyAttributeNames(t, tc.groovyFile)
			require.NotEmpty(t, read, "block policy reads no attributes; regex or template changed")
			for _, name := range read {
				assert.Truef(t, set[name],
					"block policy %s reads context attribute %q that callout template %s never sets",
					tc.groovyFile, name, tc.calloutFile)
			}
		})
	}
}

func calloutVariableNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var tmpl struct {
		Variables []struct {
			Name string `json:"name"`
		} `json:"variables"`
	}
	require.NoError(t, json.Unmarshal(data, &tmpl))
	names := make(map[string]bool, len(tmpl.Variables))
	for _, v := range tmpl.Variables {
		names[v.Name] = true
	}
	return names
}

func groovyAttributeNames(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var names []string
	seen := map[string]bool{}
	for _, m := range groovyGetAttribute.FindAllSubmatch(data, -1) {
		if name := string(m[1]); !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// TestBoomiRequestResponseBlockCoherence verifies the request and response callout
// templates extract the block decision coherently: the response attributes mirror
// the request ones (dd-block<suffix> <-> dd-resp-block<suffix>) and both read the
// same $.block... JSON paths. It catches drift where one side adds, drops, or
// renames a block field (or its source path) without the other.
func TestBoomiRequestResponseBlockCoherence(t *testing.T) {
	req := blockFieldPaths(t, "deploy/boomi/boomi-request-callout.json", "dd-block")
	resp := blockFieldPaths(t, "deploy/boomi/boomi-response-callout.json", "dd-resp-block")
	require.NotEmpty(t, req, "no dd-block* attributes found in the request callout")
	require.NotEmpty(t, resp, "no dd-resp-block* attributes found in the response callout")
	assert.Equal(t, req, resp,
		"request (dd-block*) and response (dd-resp-block*) callouts must extract the same block fields from the same JSON paths")
}

// blockFieldPaths maps each block sub-field suffix (name minus prefix) to the JSON
// path it is extracted from, e.g. "-status" -> "$.block.status".
func blockFieldPaths(t *testing.T, path, prefix string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var tmpl struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	require.NoError(t, json.Unmarshal(data, &tmpl))
	out := map[string]string{}
	for _, v := range tmpl.Variables {
		if !strings.HasPrefix(v.Name, prefix) {
			continue
		}
		m := jsonPathExpr.FindStringSubmatch(v.Value)
		require.NotNilf(t, m, "variable %q value %q has no jsonPath expression", v.Name, v.Value)
		out[strings.TrimPrefix(v.Name, prefix)] = m[1]
	}
	return out
}
