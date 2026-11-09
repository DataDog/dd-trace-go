// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package actiontest runs the shell scripts behind the repository's composite
// GitHub Actions, so their behaviour is checked without a workflow run.
package actiontest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const cacheKeyPrefixScript = "../../.github/actions/setup-go/cache-key-prefix.sh"

// emptyVariantPrefix is ddtg-cx1-<family>-<digest>, where the digest is the
// first 16 hex characters of SHA-256 over `{"variant":{}}`.
const emptyVariantPrefix = "ddtg-cx1-unit-core-c4dca83bdf2018a1"

// runCacheKeyPrefix executes the setup-go key script the way the action does
// and returns the prefix it wrote to GITHUB_OUTPUT, or its combined output and
// error when it fails.
func runCacheKeyPrefix(t *testing.T, family, variant string) (string, string, error) {
	t.Helper()
	for _, tool := range []string{"bash", "jq", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found: %v", tool, err)
		}
	}
	output := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.CommandContext(t.Context(), "bash", cacheKeyPrefixScript)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"FAMILY=" + family,
		"VARIANT=" + variant,
		"GITHUB_OUTPUT=" + output,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", string(out), err
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("reading GITHUB_OUTPUT: %v", err)
	}
	prefix, ok := strings.CutPrefix(strings.TrimSpace(string(written)), "prefix=")
	if !ok {
		t.Fatalf("GITHUB_OUTPUT has no prefix= line: %q", written)
	}
	return prefix, string(out), nil
}

func mustPrefix(t *testing.T, family, variant string) string {
	t.Helper()
	prefix, out, err := runCacheKeyPrefix(t, family, variant)
	if err != nil {
		t.Fatalf("variant %q: %v\n%s", variant, err, out)
	}
	return prefix
}

func TestCacheKeyPrefixEmptyVariant(t *testing.T) {
	if got := mustPrefix(t, "unit-core", ""); got != emptyVariantPrefix {
		t.Errorf("empty variant: got %q, want %q", got, emptyVariantPrefix)
	}
	if got := mustPrefix(t, "unit-core", "{}"); got != emptyVariantPrefix {
		t.Errorf("explicit {} variant: got %q, want %q", got, emptyVariantPrefix)
	}
}

func TestCacheKeyPrefixModuleListForms(t *testing.T) {
	array := mustPrefix(t, "unit-contrib", `{"runner":"r","modules":["b","a","c"]}`)
	text := mustPrefix(t, "unit-contrib", `{"runner":"r","modules":"c  a b"}`)
	if array != text {
		t.Errorf("array and string module lists differ: %q vs %q", array, text)
	}
	other := mustPrefix(t, "unit-contrib", `{"runner":"r","modules":["a","b"]}`)
	if array == other {
		t.Errorf("different module selections share prefix %q", array)
	}
}

func TestCacheKeyPrefixKeyOrder(t *testing.T) {
	first := mustPrefix(t, "unit-core", `{"runner":"r","tags":"standard","coverage":"core"}`)
	second := mustPrefix(t, "unit-core", `{"coverage":"core","tags":"standard","runner":"r"}`)
	if first != second {
		t.Errorf("reordered keys differ: %q vs %q", first, second)
	}
}

func TestCacheKeyPrefixRejectsNonObjectVariant(t *testing.T) {
	for _, variant := range []string{`null`, `[]`, `"text"`, `7`, `true`} {
		t.Run(variant, func(t *testing.T) {
			_, out, err := runCacheKeyPrefix(t, "unit-core", variant)
			if err == nil {
				t.Fatalf("variant %s was accepted", variant)
			}
			if !strings.Contains(out, "variant must be a JSON object") {
				t.Errorf("variant %s: output lacks the clear error:\n%s", variant, out)
			}
		})
	}
}
