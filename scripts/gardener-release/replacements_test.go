// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"os"
	"path/filepath"
	"testing"
)

func writeReplacementModule(t *testing.T, dir, modulePath, replacement string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "module " + modulePath + "\n\ngo 1.26\n"
	if replacement != "" {
		body += "\nreplace " + replacement + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func replacementFixture(t *testing.T, targetModule, replacement string) (root, app, dep string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "checkout")
	app = filepath.Join(root, "app")
	dep = filepath.Join(root, "dep")
	writeReplacementModule(t, dep, targetModule, "")
	writeReplacementModule(t, app, "example.test/app", replacement)
	return root, app, dep
}

func TestValidateLocalReplacementsAcceptsInTreeModuleRoot(t *testing.T) {
	root, _, _ := replacementFixture(t, "example.test/dep/v2", "example.test/dep/v2 => ../dep")
	if err := validateLocalReplacements(root, nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLocalReplacementsAgainstRealRepository(t *testing.T) {
	if err := validateLocalReplacements(filepath.Join("..", ".."), nil); err != nil {
		t.Fatalf("real repository replacements: %v", err)
	}
}

func TestValidateLocalReplacementsRejectsUnsafeTargetsBeforeGeneration(t *testing.T) {
	t.Run("lexical dot-dot escape", func(t *testing.T) {
		container := t.TempDir()
		root := filepath.Join(container, "checkout")
		writeReplacementModule(t, filepath.Join(container, "outside"), "example.test/dep/v2", "")
		writeReplacementModule(t, filepath.Join(root, "app"), "example.test/app", "example.test/dep/v2 => ../../outside")
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "local_replacement_escape" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("absolute external path", func(t *testing.T) {
		root, app, _ := replacementFixture(t, "example.test/dep/v2", "")
		outside := filepath.Join(t.TempDir(), "dep")
		writeReplacementModule(t, outside, "example.test/dep/v2", "")
		writeReplacementModule(t, app, "example.test/app", "example.test/dep/v2 => "+outside)
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "absolute_local_replacement" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("symlink escape", func(t *testing.T) {
		root, app, _ := replacementFixture(t, "example.test/dep/v2", "")
		outside := filepath.Join(t.TempDir(), "dep")
		writeReplacementModule(t, outside, "example.test/dep/v2", "")
		if err := os.Symlink(outside, filepath.Join(root, "linked-dep")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		writeReplacementModule(t, app, "example.test/app", "example.test/dep/v2 => ../linked-dep")
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "local_replacement_symlink_escape" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("symlinked go.mod", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "checkout")
		moduleDir := filepath.Join(root, "app")
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "go.mod")
		if err := os.WriteFile(outside, []byte("module example.test/app\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(moduleDir, "go.mod")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "replacement_modfile_symlink" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("missing target", func(t *testing.T) {
		root, _, _ := replacementFixture(t, "example.test/dep/v2", "example.test/dep/v2 => ../missing")
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "local_replacement_missing" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("target is not module root", func(t *testing.T) {
		root, app, _ := replacementFixture(t, "example.test/dep/v2", "")
		target := filepath.Join(root, "not-module")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		writeReplacementModule(t, app, "example.test/app", "example.test/dep/v2 => ../not-module")
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "local_replacement_not_module_root" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("mismatched target module", func(t *testing.T) {
		root, _, _ := replacementFixture(t, "example.test/other/v2", "example.test/dep/v2 => ../dep")
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "local_replacement_module_mismatch" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("version-qualified local target", func(t *testing.T) {
		root, _, _ := replacementFixture(t, "example.test/dep/v2", "example.test/dep/v2 => ../dep v2.9.0")
		if err := validateLocalReplacements(root, nil); ErrorCode(err) != "versioned_local_replacement" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
}

func TestValidateLocalReplacementsSkipsOnlyReviewedExcludedDirectory(t *testing.T) {
	root, _, _ := replacementFixture(t, "example.test/dep/v2", "example.test/dep/v2 => ../dep")
	writeReplacementModule(t, filepath.Join(root, "excluded"), "example.test/excluded", "example.test/dep/v2 => ../../outside")
	if err := validateLocalReplacements(root, []string{"excluded"}); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalReplacements(root, []string{"../outside"}); ErrorCode(err) != "replacement_excluded_path_invalid" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
}
