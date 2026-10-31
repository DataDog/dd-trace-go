// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"reflect"
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
/LICENSE             @DataDog/root
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
		// Exact file and suffix patterns use repository-relative paths.
		{"LICENSE", []string{"@DataDog/root"}},
		{"appsec/listener_appsec.go", []string{"@DataDog/asm-go"}},
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

func TestCodeowners_MissingFile(t *testing.T) {
	if _, err := loadCodeowners(filepath.Join(t.TempDir(), codeownersFile)); err == nil {
		t.Fatal("loadCodeowners with a missing file succeeded")
	}
}
