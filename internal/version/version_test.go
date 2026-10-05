// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package version

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		// git probably not installed; don't fail the test suite
		t.Skip(err)
	}
	out, err := exec.Command("git", "show", "-s", "--format=%ct", "HEAD", Tag).CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("unknown revision")) {
			// test passed: the tag was not found
			return
		}
		t.Skip(err)
	}
	dates := strings.Split(string(bytes.TrimSpace(out)), "\n")
	dateHEAD, err := unixDate(dates[0])
	if err != nil {
		t.Skip(err)
	}
	dateTag, err := unixDate(dates[len(dates)-1])
	if err != nil {
		t.Skip(err)
	}
	if dateTag.Before(dateHEAD) {
		t.Skipf(
			"\n(internal/version).Tag value needs to be updated!\n• %s was already released %s\n• Latest commit (HEAD) dates %s",
			Tag,
			dateTag.Format(time.Stamp),
			dateHEAD.Format(time.Stamp),
		)
	}
}

func unixDate(u string) (time.Time, error) {
	sec, err := strconv.ParseInt(u, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

func TestParseVersion(t *testing.T) {
	tc := []struct {
		version string
		major   int
		minor   int
		patch   int
		rc      int
	}{
		{"v1.2.3-rc.12", 1, 2, 3, 12},
		{"v2.0.0-rc.1", 2, 0, 0, 1},
		{"v2.1.0-dev", 2, 1, 0, 0},
		{"v2.1.0-alpha", 2, 1, 0, 0},
		{"v2.1.0-alpha.21", 2, 1, 0, 21},
		{"v2.5.0-rc.11", 2, 5, 0, 11},
		{"v2.1.0-beta.9", 2, 1, 0, 9},
	}
	for _, c := range tc {
		v := parseVersion(c.version)
		if v.Major != c.major {
			t.Errorf("Major is %d", v.Major)
		}
		if v.Minor != c.minor {
			t.Errorf("Minor is %d", v.Minor)
		}
		if v.Patch != c.patch {
			t.Errorf("Patch is %d", v.Patch)
		}
		if v.RC != c.rc {
			t.Errorf("RC is %d", v.RC)
		}
	}
}

func BenchmarkParseVersion(b *testing.B) {
	version := "v2.1.0-rc.21"
	for b.Loop() {
		v := parseVersion(version)
		if got, want := v.Major, 2; got != want {
			b.Fatalf("got %d, want %d", got, want)
		}
		if got, want := v.Minor, 1; got != want {
			b.Fatalf("got %d, want %d", got, want)
		}
		if got, want := v.Patch, 0; got != want {
			b.Fatalf("got %d, want %d", got, want)
		}
		if got, want := v.RC, 21; got != want {
			b.Fatalf("got %d, want %d", got, want)
		}
	}
}
