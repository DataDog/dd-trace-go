// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeowners

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func loadFixture(t *testing.T, content string) *CodeOwners {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CODEOWNERS")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	owners, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	return owners
}

func TestNew(t *testing.T) {
	owners := loadFixture(t, `[Section 1]
/path/to/file @owner1 @owner2
/path/to/* @owner3

[section 1]
/another/path @owner4
`)
	section := owners.GetSection("Section 1")
	if section == nil {
		t.Fatal("section not found")
	}
	if len(section.Entries) != 3 {
		t.Fatalf("section has %d entries, want 3", len(section.Entries))
	}
	if _, err := New(""); err == nil {
		t.Fatal("New with an empty path succeeded")
	}
}

func TestMatch(t *testing.T) {
	owners := loadFixture(t, `/path/ @broad
/path/to/file @exact1 @exact2
*.md @docs
`)
	cases := []struct {
		path string
		want []string
	}{
		{"/path/to/file", []string{"@exact1", "@exact2"}},
		{"/path/to/other", []string{"@broad"}},
		{"/docs/README.md", []string{"@docs"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			entry, ok := owners.Match(tc.path)
			if !ok {
				t.Fatal("Match returned no entry")
			}
			if !reflect.DeepEqual(entry.Owners, tc.want) {
				t.Errorf("owners = %v, want %v", entry.Owners, tc.want)
			}
		})
	}
	if entry, ok := owners.Match("/unowned"); ok || entry != nil {
		t.Errorf("unowned match = %#v, %v", entry, ok)
	}
}

func TestMatchCombinesSections(t *testing.T) {
	owners := loadFixture(t, `[one]
/path/ @one
[two]
/path/ @two
`)
	entry, ok := owners.Match("/path/file")
	if !ok {
		t.Fatal("Match returned no entry")
	}
	if got, want := entry.Owners, []string{"@two", "@one"}; !reflect.DeepEqual(got, want) {
		t.Errorf("owners = %v, want %v", got, want)
	}
}

func TestNilDoesNotMatch(t *testing.T) {
	var owners *CodeOwners
	if entry, ok := owners.Match("/source.go"); ok || entry != nil {
		t.Errorf("nil match = %#v, %v", entry, ok)
	}
}

func TestGetOwnersString(t *testing.T) {
	entry := Entry{Owners: []string{"@one", "@two"}}
	if got, want := entry.GetOwnersString(), `["@one","@two"]`; got != want {
		t.Errorf("GetOwnersString() = %q, want %q", got, want)
	}
	if got := (&Entry{}).GetOwnersString(); got != "" {
		t.Errorf("empty GetOwnersString() = %q", got)
	}
}
