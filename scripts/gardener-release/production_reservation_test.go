// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrustedTagCompletenessUsesExactManifest(t *testing.T) {
	line, err := parseReleaseLine("v2.11")
	if err != nil {
		t.Fatal(err)
	}
	version := "v2.11.0-rc.1"
	commit := strings.Repeat("a", 40)
	expected := []string{version, "contrib/a/" + version, "contrib/new/" + version}
	cases := []struct {
		name string
		tags map[string]string
		want bool
	}{
		{name: "root only", tags: map[string]string{"refs/tags/" + version: commit}, want: true},
		{name: "strict subset", tags: map[string]string{"refs/tags/" + version: commit, "refs/tags/contrib/a/" + version: commit}, want: true},
		{name: "new module never appeared in history", tags: map[string]string{
			"refs/tags/" + version:           commit,
			"refs/tags/contrib/a/" + version: commit,
			"refs/tags/v2.10.0":              commit,
			"refs/tags/contrib/a/v2.10.0":    commit,
		}, want: true},
		{name: "complete exact manifest", tags: map[string]string{
			"refs/tags/" + version:             commit,
			"refs/tags/contrib/a/" + version:   commit,
			"refs/tags/contrib/new/" + version: commit,
		}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			incomplete, err := trustedIncompleteTagVersions(test.tags, map[string]bool{version: true}, map[string]bool{version: true}, map[string]string{version: commit}, line, func(gotVersion, gotCommit string) ([]string, error) {
				if gotVersion != version || gotCommit != commit {
					t.Fatalf("resolver got %q/%q", gotVersion, gotCommit)
				}
				return expected, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got := len(incomplete) == 1 && incomplete[0] == version
			if got != test.want {
				t.Fatalf("incomplete = %v, want marked=%t", incomplete, test.want)
			}
		})
	}
}

func TestTrustedTagCompletenessScalesInventoryButPlansRelevantRootsOnly(t *testing.T) {
	line, err := parseReleaseLine("v2.11")
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("a", 40)
	version := "v2.11.0-rc.1"
	tags := map[string]string{"refs/tags/" + version: commit, "refs/tags/contrib/a/" + version: commit}
	roots := map[string]bool{version: true}
	for i := 0; i < 10001; i++ {
		old := fmt.Sprintf("v1.%d.0", i)
		tags["refs/tags/"+old] = commit
		roots[old] = true
	}
	calls := 0
	incomplete, err := trustedIncompleteTagVersions(tags, roots, map[string]bool{version: true}, map[string]string{version: commit}, line, func(_, _ string) ([]string, error) {
		calls++
		return []string{version, "contrib/a/" + version}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(incomplete) != 0 || calls != 1 {
		t.Fatalf("incomplete=%v resolver calls=%d, want none/1", incomplete, calls)
	}
}

func TestTrustedTagCompletenessFailsClosedWithoutPeeledRoot(t *testing.T) {
	line, err := parseReleaseLine("v2.11")
	if err != nil {
		t.Fatal(err)
	}
	version := "v2.11.0"
	_, err = trustedIncompleteTagVersions(map[string]string{"refs/tags/" + version: strings.Repeat("a", 40)}, map[string]bool{version: true}, nil, nil, line, func(_, _ string) ([]string, error) {
		t.Fatal("resolver must not run without a peeled root commit")
		return nil, nil
	})
	if ErrorCode(err) != "root_tag_commit_unavailable" {
		t.Fatalf("error = %q, want root_tag_commit_unavailable", ErrorCode(err))
	}
}
