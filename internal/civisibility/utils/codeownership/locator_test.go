// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRules(t *testing.T, root, folder, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, folder, "CODEOWNERS"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestOfficialLocationPriority(t *testing.T) {
	for _, tc := range []struct {
		provider string
		folders  []string
	}{
		{"github", []string{".github", "", "docs"}}, {"gitlab", []string{"", "docs", ".gitlab"}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			workspace := t.TempDir()
			// Discovery returns physical paths. TempDir may retain /var's symlink on
			// macOS or a short/case-normalized spelling on Windows.
			root, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			for i, folder := range tc.folders {
				writeRules(t, root, folder, "* @owner"+string(rune('a'+i)))
			}
			for i, folder := range tc.folders {
				resolver, err := Discover(Locations{Workspace: workspace, Provider: tc.provider})
				if err != nil {
					t.Fatal(err)
				}
				if resolver == nil || resolver.File() != filepath.Join(root, folder, "CODEOWNERS") {
					t.Fatalf("priority: %+v", resolver)
				}
				got, _ := resolver.Match("/a.go")
				if got.FirstOwner() != "@owner"+string(rune('a'+i)) {
					t.Fatal(got.Tag())
				}
				if err := os.Remove(filepath.Join(root, folder, "CODEOWNERS")); err != nil {
					t.Fatal(err)
				}
			}
			resolver, err := Discover(Locations{Workspace: workspace, Provider: tc.provider})
			if err != nil || resolver != nil {
				t.Fatalf("%v %v", resolver, err)
			}
		})
	}
}
func TestDetectDialect(t *testing.T) {
	for _, tc := range []struct {
		repository, provider string
		dialect              Dialect
	}{
		{"https://gitlab.com/org/repo.git", "jenkins", GitLab}, {"git@gitlab.example.com:org/repo.git", "jenkins", GitLab},
		{"https://code.gitlab.example/org/repo", "github", GitLab}, {"ssh://git@GITHUB.COM/org/repo", "gitlab", GitHub},
		{"https://unknown.example/org/repo", "gitlab", GitLab}, {"https://unknown.example/org/repo", "github", GitHub},
	} {
		t.Run(tc.repository, func(t *testing.T) {
			if got := detectDialect(t.TempDir(), tc.repository, tc.provider); got != tc.dialect {
				t.Fatalf("got=%v want=%v", got, tc.dialect)
			}
		})
	}
	root := t.TempDir()
	writeRules(t, root, ".gitlab", "[Go] @team\n*.go")
	if detectDialect(root, "", "jenkins") != GitLab {
		t.Fatal("GitLab-specific location was ignored")
	}
	writeRules(t, root, ".github", "* @team")
	if detectDialect(root, "", "jenkins") != GitHub {
		t.Fatal("ambiguous locations changed default")
	}
}
func TestOtherDialectLocationIsIgnored(t *testing.T) {
	for _, tc := range []struct{ provider, folder string }{{"github", ".gitlab"}, {"gitlab", ".github"}} {
		root := t.TempDir()
		writeRules(t, root, tc.folder, "* @wrong")
		resolver, err := Discover(Locations{Workspace: root, Provider: tc.provider})
		if err != nil || resolver != nil {
			t.Fatalf("other dialect file loaded: %v %v", resolver, err)
		}
	}
}
func TestRepositoryRootAndWorkspaceRebasing(t *testing.T) {
	for _, gitFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "worktree-file"}[gitFile], func(t *testing.T) {
			workspaceRoot := t.TempDir()
			root, err := filepath.EvalSymlinks(workspaceRoot)
			if err != nil {
				t.Fatal(err)
			}
			gitPath := filepath.Join(root, ".git")
			if gitFile {
				if err := os.WriteFile(gitPath, []byte("gitdir: outside"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(gitPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			workspace := filepath.Join(workspaceRoot, "src")
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			writeRules(t, root, "", "* @repository\n/src/pkg/ @package")
			writeRules(t, workspace, "", "* @nested")
			resolver, err := Discover(Locations{Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			if resolver == nil || resolver.Root() != root {
				t.Fatalf("root: %+v", resolver)
			}
			got, _ := resolver.Match("/pkg/main.go")
			if got.FirstOwner() != "@package" {
				t.Fatal(got.Tag())
			}
			got, _ = resolver.MatchDirectory("/pkg")
			if got.FirstOwner() != "@package" {
				t.Fatal(got.Tag())
			}
			if _, ok := resolver.Match("../outside.go"); ok {
				t.Fatal("outside repository match")
			}
			if _, ok := resolver.MatchDirectory("../outside"); ok {
				t.Fatal("outside repository directory match")
			}
		})
	}
}
func TestDiscoveryDoesNotSwitchAfterReadError(t *testing.T) {
	root := t.TempDir()
	writeRules(t, root, "", "* @fallback")
	github := filepath.Join(root, ".github")
	if err := os.Mkdir(github, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(github, "CODEOWNERS")
	if err := os.Symlink("CODEOWNERS", file); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if resolver, err := Discover(Locations{Workspace: root, Provider: "github"}); resolver != nil || err == nil {
		t.Fatalf("read error changed selection: %v %v", resolver, err)
	}
}
func TestDiscoverySnapshotAndOversizedSelection(t *testing.T) {
	root := t.TempDir()
	writeRules(t, root, "", "* @fallback")
	writeRules(t, root, ".github", "* @first")
	resolver, err := Discover(Locations{Workspace: root})
	if err != nil {
		t.Fatal(err)
	}
	writeRules(t, root, ".github", "* @second")
	got, _ := resolver.Match("/a.go")
	if got.FirstOwner() != "@first" {
		t.Fatal("snapshot changed")
	}
	file, err := os.OpenFile(filepath.Join(root, ".github", "CODEOWNERS"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(GitHubMaximumFileSize + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	resolver, err = Discover(Locations{Workspace: root})
	if err != nil {
		t.Fatal(err)
	}
	if resolver == nil || resolver.Diagnostics() != 0 {
		t.Fatalf("oversized selection: %v", resolver)
	}
	if _, ok := resolver.Match("/a.go"); ok {
		t.Fatal("fell back to lower-priority file")
	}
}

// DiscoverIn reads only the given directory: it neither selects the enclosing
// Git root's file nor rebases paths onto it.
func TestDiscoverInUsesOnlyTheDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	writeRules(t, root, "", "* @root")
	writeRules(t, root, "pkg", "* @global\n/pkg/a_test.go @specific")
	resolver, err := DiscoverIn(filepath.Join(root, "pkg"), Locations{})
	if err != nil || resolver == nil {
		t.Fatalf("%v %v", resolver, err)
	}
	if got, _ := resolver.Match("/pkg/a_test.go"); got.FirstOwner() != "@specific" {
		t.Fatalf("owner %q", got.FirstOwner())
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	if resolver, err := DiscoverIn(empty, Locations{}); err != nil || resolver != nil {
		t.Fatalf("parent file selected: %v %v", resolver, err)
	}
}
