// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustLoadCodeowners(t *testing.T, content string) *codeowners {
	t.Helper()
	path := filepath.Join(t.TempDir(), codeownersFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture CODEOWNERS: %v", err)
	}
	co, err := loadCodeowners(path)
	if err != nil {
		t.Fatalf("loadCodeowners: %v", err)
	}
	return co
}

const codeownersFixture = `# comment line
/internal/          @DataDog/guild
/internal/log/      @DataDog/log-team
/ddtrace/           @DataDog/apm-go @DataDog/apm-idm-go
*appsec.go           @DataDog/asm-go
*config              @DataDog/config-team
/LICENSE             @DataDog/root
/otelc/              @DataDog/guild
`

func TestCodeowners_Matching(t *testing.T) {
	co := mustLoadCodeowners(t, codeownersFixture)
	cases := []struct {
		path string
		want []string
	}{
		// A narrower later directory entry wins over the broader earlier one.
		{"internal/log/log.go", []string{"@DataDog/log-team"}},
		{"internal/env/config.go", []string{"@DataDog/guild"}},
		// Exact file match.
		{"LICENSE", []string{"@DataDog/root"}},
		// Suffix match on the basename.
		{"appsec/listener_appsec.go", []string{"@DataDog/asm-go"}},
		// Suffix match on a directory component implies the whole subtree.
		{"internal/config/reader.go", []string{"@DataDog/config-team"}},
		{"internal/config/sub/reader.go", []string{"@DataDog/config-team"}},
		// Multiple owners are preserved as a group.
		{"ddtrace/tracer/tracer.go", []string{"@DataDog/apm-go", "@DataDog/apm-idm-go"}},
		// Files owned by nobody fall in a placeholder group.
		{"orphan/thing.go", []string{unownedPlaceholder}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := co.ownersFor(tc.path)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ownersFor(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestCodeowners_ParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		errPart string
	}{
		{"no owners", "/internal/ \n", "has no owners"},
		// Mirrors the specific rejections in scripts/check_codeowners.go, so
		// the two parsers cannot drift apart.
		{"wildcard in anchored path", "/a?b/ @DataDog/guild\n", "wildcards are not supported in anchored paths"},
		{"unanchored path", "somefile.go @DataDog/guild\n", "pattern must be anchored with a leading"},
		{"double star suffix", "*a*b @DataDog/guild\n", `are gitignore wildcards that CI Visibility treats as literal characters`},
		{"slash in suffix", "*sec/go @DataDog/guild\n", `a "*" pattern must be a bare suffix containing no "/"`},
		{"star catch-all", "* @DataDog/guild\n", `the "*" catch-all is not allowed`},
		{"slash catch-all", "/ @DataDog/guild\n", `the "/" catch-all is not allowed`},
		{"wildcard slash pattern", "/*/ @DataDog/guild\n", "wildcards are not supported in anchored paths"},
		{"at in path", "/a@b/ @DataDog/guild\n", `"@" in a path is parsed as an owner`},
		{"missing file", "", "reading"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, codeownersFile)
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("writing fixture CODEOWNERS: %v", err)
				}
			}
			_, err := loadCodeowners(path)
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("loadCodeowners error = %v, want one containing %q", err, tc.errPart)
			}
		})
	}
}
