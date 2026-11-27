// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"go/build"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestLibraryRegistryValidation(t *testing.T) {
	t.Parallel()

	check := func() {}
	for _, tc := range []struct {
		name        string
		libraryName string
		check       func()
	}{
		{name: "empty name", check: check},
		{name: "nil check", libraryName: "nil"},
		{name: "duplicate name", libraryName: "test", check: check},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := newLibraryRegistry()
			reg.register("test", check)
			defer func() {
				if recover() == nil {
					t.Error("register did not panic")
				}
			}()
			reg.register(tc.libraryName, tc.check)
		})
	}
}

func TestLibraryRegistryOrder(t *testing.T) {
	t.Parallel()

	reg := newLibraryRegistry()
	var got []string
	reg.register("b", func() { got = append(got, "b") })
	reg.register("a", func() { got = append(got, "a") })
	reg.run()

	want := []string{"a", "b"}
	if !slices.Equal(got, want) {
		t.Fatalf("run order = %v, want %v", got, want)
	}
}

func TestUseLibraries(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("build.ImportDir(.): %v", err)
	}
	var wantKeys []string
	for _, f := range pkg.GoFiles {
		if rest, ok := strings.CutPrefix(f, "lib_"); ok {
			wantKeys = append(wantKeys, strings.TrimSuffix(rest, ".go"))
		}
	}
	if len(wantKeys) == 0 {
		t.Fatal("found no lib_*.go files")
	}
	slices.Sort(wantKeys)

	gotKeys := slices.Sorted(maps.Keys(defaultRegistry.checks))
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("registered libraries = %v, want %v (each lib_<name>.go must register <name>)", gotKeys, wantKeys)
	}
	useLibraries()
}
