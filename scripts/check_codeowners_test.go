// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build ignore

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnchoredFilenamePrefix(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		path    string
		matches bool
	}{
		{"/src/ci_*", "src/ci_new.go", true},
		{"/src/ci_*", "src/ci_new_test.go", true},
		{"/src/ci_*", "src/ci_", true},
		{"/src/ci_*", "src/ci_group/nested.go", true},
		{"/ci_*", "ci_root.go", true},
		{"/src/ci_*", "src/app.go", false},
		{"/src/ci_*", "src/ci.go", false},
		{"/src/ci_*", "src/CI_new.go", false},
		{"/src/ci_*", "src/nested/ci_new.go", false},
		{"/src/ci_*", "other/src/ci_new.go", false},
		{"/src/ci_*", "other/ci_new.go", false},
	} {
		t.Run(tc.pattern+":"+tc.path, func(t *testing.T) {
			k, match, err := classify(tc.pattern)
			require.NoError(t, err)
			r := rule{kind: k, match: match}
			assert.Equal(t, tc.matches, r.matches(tc.path))
		})
	}
}

func TestRejectUnsupportedWildcards(t *testing.T) {
	for _, pattern := range []string{"*", "/*", "/src/*", "/src/ci_**", "/src/ci_?*", "/src/ci_[ab]*", "/src/ci_\\*", "/src/ci_*.go", "/src/ci_*/file*", "ci_*", "/src/@ci_*"} {
		t.Run(pattern, func(t *testing.T) {
			_, _, err := classify(pattern)
			require.Error(t, err)
		})
	}
}

func TestAnchoredFilenamePrefixPrecedence(t *testing.T) {
	for _, tc := range []struct {
		content string
		owner   string
	}{
		{"/src/ @directory\n/src/ci_* @prefix\n/src/ci_exact.go @exact\n", "@exact"},
		{"/src/ @directory\n/src/ci_exact.go @exact\n/src/ci_* @prefix\n", "@prefix"},
		{"/src/ci_* @prefix\n/src/ @directory\n", "@directory"},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CODEOWNERS")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			rules, err := parse(path)
			require.NoError(t, err)
			owners, _, ok := resolve(rules, "src/ci_exact.go")
			require.True(t, ok)
			assert.Equal(t, []string{tc.owner}, owners)
		})
	}
}
