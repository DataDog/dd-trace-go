// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package version

import (
	"bytes"
	"os/exec"
	"runtime/debug"
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

func TestFindV1Version(t *testing.T) {
	tests := []struct {
		deps     []*debug.Module
		expected *v1version
	}{
		{
			deps: []*debug.Module{
				{Path: "gopkg.in/DataDog/dd-trace-go.v1", Version: "v1.2.3-rc.12"},
			},
			expected: &v1version{
				Version: "v1.2.3-rc.12",
			},
		},
		{
			deps: []*debug.Module{
				{Path: "gopkg.in/DataDog/dd-trace-go.v1", Version: "v1.74.0"},
			},
			expected: &v1version{
				Version:      "v1.74.0",
				Transitional: true,
			},
		},
		{
			deps: []*debug.Module{
				{Path: "gopkg.in/DataDog/dd-trace-go.v1", Version: "v1.73.1"},
			},
			expected: &v1version{
				Version: "v1.73.1",
			},
		},
		{
			deps:     []*debug.Module{},
			expected: nil,
		},
		{
			deps: []*debug.Module{
				{Path: "github.com/DataDog/dd-trace-go/v2", Version: "v2.0.0"},
			},
			expected: nil,
		},
	}
	for _, c := range tests {
		vt := findV1Version(c.deps)
		if c.expected == nil {
			if vt != nil {
				t.Fatalf("got %v, expected nil", vt)
			}
			continue
		}
		if vt == nil {
			t.Fatalf("got nil, expected *v1version")
		}
		if vt.Version != c.expected.Version {
			t.Fatalf("got %s, expected %s", vt.Version, c.expected.Version)
		}
		if vt.Transitional != c.expected.Transitional {
			t.Fatalf("got %t, expected %t", vt.Transitional, c.expected.Transitional)
		}
	}
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
		{"v2.1.0-alpha.21", 2, 1, 0, 21},
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
