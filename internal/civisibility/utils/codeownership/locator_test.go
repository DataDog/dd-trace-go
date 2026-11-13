// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"os"
	"path/filepath"
	"runtime"
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
				resolver, err := DiscoverIn(workspace, Locations{Provider: tc.provider})
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
			resolver, err := DiscoverIn(workspace, Locations{Provider: tc.provider})
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
			if got, err := detectDialect(t.TempDir(), tc.repository, tc.provider); err != nil || got != tc.dialect {
				t.Fatalf("got=%v want=%v err=%v", got, tc.dialect, err)
			}
		})
	}
	root := t.TempDir()
	writeRules(t, root, ".gitlab", "[Go] @team\n*.go")
	if got, err := detectDialect(root, "", "jenkins"); err != nil || got != GitLab {
		t.Fatal("GitLab-specific location was ignored")
	}
	writeRules(t, root, ".github", "* @team")
	if got, err := detectDialect(root, "", "jenkins"); err != nil || got != GitHub {
		t.Fatal("ambiguous locations changed default")
	}

	// A directory reached through a symlink, as macOS's /var is, still
	// contains its markers.
	linked := filepath.Join(t.TempDir(), "linked")
	target := t.TempDir()
	writeRules(t, target, ".gitlab", "[Go] @team\n*.go")
	if err := os.Symlink(target, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := detectDialect(linked, "", "jenkins"); err != nil || got != GitLab {
		t.Fatalf("marker behind a linked directory ignored: %v %v", got, err)
	}
}
func TestOtherDialectLocationIsIgnored(t *testing.T) {
	for _, tc := range []struct{ provider, folder string }{{"github", ".gitlab"}, {"gitlab", ".github"}} {
		root := t.TempDir()
		writeRules(t, root, tc.folder, "* @wrong")
		resolver, err := DiscoverIn(root, Locations{Provider: tc.provider})
		if err != nil || resolver != nil {
			t.Fatalf("other dialect file loaded: %v %v", resolver, err)
		}
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
	if resolver, err := DiscoverIn(root, Locations{Provider: "github"}); resolver != nil || err == nil {
		t.Fatalf("read error changed selection: %v %v", resolver, err)
	}
}

// An unreadable marker used to pick the dialect is a read error too: discovery
// fails, to be retried, rather than settling on the other host's locations.
func TestDialectMarkerReadErrorIsReturned(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".gitlab"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("CODEOWNERS", filepath.Join(root, ".gitlab", "CODEOWNERS")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if resolver, err := DiscoverIn(root, Locations{Provider: "jenkins"}); resolver != nil || err == nil {
		t.Fatalf("marker error ignored: %v %v", resolver, err)
	}
	if resolver, err := DiscoverIn(root, Locations{Provider: "github"}); resolver != nil || err != nil {
		t.Fatalf("marker read although the provider decides: %v %v", resolver, err)
	}
}
func TestDiscoverySnapshotAndOversizedSelection(t *testing.T) {
	root := t.TempDir()
	writeRules(t, root, "", "* @fallback")
	writeRules(t, root, ".github", "* @first")
	resolver, err := DiscoverIn(root, Locations{})
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
	resolver, err = DiscoverIn(root, Locations{})
	if err != nil {
		t.Fatal(err)
	}
	if resolver == nil || resolver.Diagnostics() != 0 || !resolver.Ignored() {
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
	if _, ok := resolver.Match("/../pkg/a_test.go"); ok {
		t.Fatal("path outside the directory matched")
	}
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	if resolver, err := DiscoverIn(empty, Locations{}); err != nil || resolver != nil {
		t.Fatalf("parent file selected: %v %v", resolver, err)
	}
}

// A device or named pipe in a CODEOWNERS location could block discovery or
// never end, so discovery skips it and Load rejects it.
func TestNonRegularFilesAreNotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no device file to link to")
	}
	link := func(t *testing.T, root, folder string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
		filename := filepath.Join(root, folder, "CODEOWNERS")
		if err := os.Symlink(os.DevNull, filename); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		return filename
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeRules(t, root, "", "* @root")
	device := link(t, root, ".github")
	if _, err := Load(device, GitHub); err == nil {
		t.Fatal("device file loaded")
	}
	resolver, err := DiscoverIn(root, Locations{Provider: "github"})
	if err != nil || resolver == nil || resolver.File() != filepath.Join(root, "CODEOWNERS") {
		t.Fatalf("%v %v", resolver, err)
	}
	if got, _ := resolver.Match("/a.go"); got.FirstOwner() != "@root" {
		t.Fatalf("owner %q", got.FirstOwner())
	}
	other := t.TempDir()
	link(t, other, ".gitlab")
	if got, err := detectDialect(other, "", "jenkins"); err != nil || got != GitHub {
		t.Fatal("device file selected the GitLab dialect")
	}
}

// A symlink is followed only while it stays inside the directory: a file
// outside it could be anything readable, such as /proc/kmsg, which blocks.
func TestSymlinksStayInsideTheDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeRules(t, root, "", "* @root")
	writeRules(t, root, "owners", "* @inside")
	outside := t.TempDir()
	writeRules(t, outside, "", "* @outside")
	if err := os.Mkdir(filepath.Join(root, ".github"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".github", "CODEOWNERS")
	if err := os.Symlink(filepath.Join(outside, "CODEOWNERS"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolver, err := DiscoverIn(root, Locations{Provider: "github"})
	if err != nil || resolver == nil {
		t.Fatalf("%v %v", resolver, err)
	}
	if got, _ := resolver.Match("/a.go"); got.FirstOwner() != "@root" {
		t.Fatalf("outside file selected: %q", got.FirstOwner())
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "owners", "CODEOWNERS"), link); err != nil {
		t.Fatal(err)
	}
	resolver, err = DiscoverIn(root, Locations{Provider: "github"})
	if err != nil || resolver == nil {
		t.Fatalf("%v %v", resolver, err)
	}
	if got, _ := resolver.Match("/a.go"); got.FirstOwner() != "@inside" {
		t.Fatalf("inside link not followed: %q", got.FirstOwner())
	}

	// A skipped link does not select the dialect either: the GitLab file is
	// the only one discovery can read.
	gitLab, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeRules(t, gitLab, ".gitlab", "* @gitlab")
	if err := os.Mkdir(filepath.Join(gitLab, ".github"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "CODEOWNERS"), filepath.Join(gitLab, ".github", "CODEOWNERS")); err != nil {
		t.Fatal(err)
	}
	resolver, err = DiscoverIn(gitLab, Locations{Provider: "jenkins"})
	if err != nil || resolver == nil || resolver.Dialect() != GitLab {
		t.Fatalf("%v %v", resolver, err)
	}
	if got, _ := resolver.Match("/a.go"); got.FirstOwner() != "@gitlab" {
		t.Fatalf("owner %q", got.FirstOwner())
	}
}
