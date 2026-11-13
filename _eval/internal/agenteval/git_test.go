// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo builds a throwaway git repository with one commit.
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := runGit(ctx, dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeFiles(t, dir, files)
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runGit(ctx, dir, "commit", "-q", "-m", "initial"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMaterializeTreeHasNoHistory(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{
		"contrib/thing/thing.go": "package thing\n",
		"README.md":              "hi\n",
	})
	// Delete the file in a second commit. An agent asked to rebuild it must not be
	// able to recover it, which is the whole point of archiving rather than cloning.
	if err := os.Remove(filepath.Join(repo, "contrib/thing/thing.go")); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(ctx, repo, "remove thing"); err != nil {
		t.Fatal(err)
	}
	head, err := ResolveRef(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "ws")
	if err := MaterializeTree(ctx, repo, head, dest); err != nil {
		t.Fatalf("materialise: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("expected the tree contents to be present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "contrib/thing/thing.go")); !os.IsNotExist(err) {
		t.Errorf("deleted file should be absent, got err=%v", err)
	}

	out, err := runGit(ctx, dest, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if strings.TrimSpace(out) != "1" {
		t.Errorf("workspace history has %s commits, want exactly 1 so prior content is unreachable", strings.TrimSpace(out))
	}
	// The tar must not have carried the original .git directory across.
	if entries, err := os.ReadDir(filepath.Join(dest, ".git")); err != nil || len(entries) == 0 {
		t.Errorf("workspace should have its own fresh .git: %v", err)
	}
}

func TestMaterializeTreeRefusesNonEmptyDest(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{"a.txt": "a\n"})
	dest := t.TempDir()
	writeFiles(t, dest, map[string]string{"existing": "x\n"})

	if err := MaterializeTree(ctx, repo, "HEAD", dest); err == nil {
		t.Fatal("expected an error for a non-empty destination")
	}
}

func TestDiffCapturesNewAndChangedFiles(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{
		"keep.go":    "package keep\n",
		".gitignore": "ignored/\n",
	})

	writeFiles(t, repo, map[string]string{
		"keep.go":           "package keep\n\nfunc New() {}\n",
		"added.go":          "package added\n",
		"ignored/build.out": "junk\n",
	})

	diff, changed, err := Diff(ctx, repo)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !containsStr(changed, "added.go") {
		t.Errorf("changed = %v, want a newly created file to be included", changed)
	}
	if !containsStr(changed, "keep.go") {
		t.Errorf("changed = %v, want the modified file", changed)
	}
	// .gitignore still applies, so build output does not count as the agent's work.
	if containsStr(changed, "ignored/build.out") {
		t.Errorf("changed = %v, want gitignored paths excluded", changed)
	}
	if !strings.Contains(diff, "func New() {}") {
		t.Errorf("diff should contain the change, got:\n%s", diff)
	}
	if n := DiffLineCount(diff); n < 2 {
		t.Errorf("DiffLineCount = %d, want at least the two added lines", n)
	}
}

func TestDiffLineCountIgnoresHeaders(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/x.go b/x.go",
		"--- a/x.go",
		"+++ b/x.go",
		"@@ -1,2 +1,3 @@",
		" unchanged",
		"+added one",
		"+added two",
		"-removed one",
	}, "\n")
	if got, want := DiffLineCount(diff), 3; got != want {
		t.Errorf("DiffLineCount = %d, want %d", got, want)
	}
}

func TestPathMatches(t *testing.T) {
	tests := []struct {
		path     string
		prefixes []string
		want     bool
	}{
		{"contrib/twmb/franz-go/kgo.go", []string{"contrib/twmb/franz-go"}, true},
		{"contrib/twmb/franz-go/kgo.go", []string{"contrib/twmb/franz-go/"}, true},
		{"go.sum", []string{"go.sum"}, true},
		// A prefix must stop at a path separator, otherwise "contrib/twmb/franz-go"
		// would also match a sibling directory like franz-go-extras.
		{"contrib/twmb/franz-go-extras/x.go", []string{"contrib/twmb/franz-go"}, false},
		{"go.sums", []string{"go.sum"}, false},
		{"contrib/other/x.go", []string{"contrib/twmb/franz-go", "instrumentation/"}, false},
		{"instrumentation/packages.go", []string{"contrib/twmb/franz-go", "instrumentation/"}, true},
		{"anything", nil, false},
	}
	for _, tt := range tests {
		if got := PathMatches(tt.path, tt.prefixes); got != tt.want {
			t.Errorf("PathMatches(%q, %v) = %v, want %v", tt.path, tt.prefixes, got, tt.want)
		}
	}
}

func TestAllPrefixesTouched(t *testing.T) {
	changed := []string{"contrib/twmb/franz-go/kgo.go", "instrumentation/packages.go"}

	if !AllPrefixesTouched(changed, []string{"contrib/twmb/franz-go", "instrumentation/packages.go"}) {
		t.Error("want true when every prefix has a change under it")
	}
	// Touching only one of two required areas is not success.
	if AllPrefixesTouched(changed, []string{"contrib/twmb/franz-go", "orchestrion/"}) {
		t.Error("want false when a required prefix has no change")
	}
}

func TestAnyPathMatches(t *testing.T) {
	changed := []string{"contrib/x/a.go", "go.sum"}
	if !AnyPathMatches(changed, []string{"go.sum", ".github/"}) {
		t.Error("want true when a forbidden path was touched")
	}
	if AnyPathMatches(changed, []string{".github/", "ddtrace/tracer/"}) {
		t.Error("want false when no forbidden path was touched")
	}
}

func containsStr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
