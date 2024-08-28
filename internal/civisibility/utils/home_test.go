// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func patchEnv(key, value string) func() {
	bck := os.Getenv(key)
	deferFunc := func() {
		_ = os.Setenv(key, bck)
	}

	if value != "" {
		_ = os.Setenv(key, value)
	} else {
		_ = os.Unsetenv(key)
	}

	return deferFunc
}

func TestDir(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	dir := getHomeDir()
	if u.HomeDir != dir {
		t.Fatalf("%#v != %#v", u.HomeDir, dir)
	}

	defer patchEnv("HOME", "")()
	dir = getHomeDir()
	if u.HomeDir != dir {
		t.Fatalf("%#v != %#v", u.HomeDir, dir)
	}
}

func TestExpand(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	cases := []struct {
		Input  string
		Output string
	}{
		{
			"/foo",
			"/foo",
		},

		{
			"~/foo",
			filepath.Join(u.HomeDir, "foo"),
		},

		{
			"",
			"",
		},

		{
			"~",
			u.HomeDir,
		},

		{
			"~foo/foo",
			"~foo/foo",
		},
	}

	for _, tc := range cases {
		actual := ExpandPath(tc.Input)
		if actual != tc.Output {
			t.Fatalf("Input: %#v\n\nOutput: %#v", tc.Input, actual)
		}
	}

	defer patchEnv("HOME", "/custom/path/")()
	expected := filepath.Join("/", "custom", "path", "foo/bar")
	actual := ExpandPath("~/foo/bar")
	if actual != expected {
		t.Errorf("Expected: %v; actual: %v", expected, actual)
	}
}
