// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

func TestParseModFileExtractsModuleRequireAndReplace(t *testing.T) {
	data := []byte(`module example.com/root/moduleB/v2

go 1.26.0

require (
	example.com/root/moduleA/v2 v2.0.0
	example.com/root/v2 v2.0.0
	external.example.com/pkg v1.2.3 // indirect
)

replace example.com/root/v2 => ./..

replace example.com/root/moduleA/v2 => ../moduleA
`)
	mod, err := ParseModFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if mod.ModulePath != "example.com/root/moduleB/v2" || mod.Go != "1.26.0" {
		t.Fatalf("unexpected module/go: %#v", mod)
	}
	if len(mod.Require) != 3 {
		t.Fatalf("require count = %d, want 3: %#v", len(mod.Require), mod.Require)
	}
	if mod.Require[2].Path != "external.example.com/pkg" || !mod.Require[2].Indirect {
		t.Fatalf("indirect requirement not parsed: %#v", mod.Require[2])
	}
	rep, ok := mod.FindReplace("example.com/root/v2")
	if !ok || rep.NewPath != "./.." || !rep.IsFilesystemReplacement() {
		t.Fatalf("unexpected replace for root: %#v ok=%v", rep, ok)
	}
	rep2, ok := mod.FindReplace("example.com/root/moduleA/v2")
	if !ok || rep2.NewPath != "../moduleA" {
		t.Fatalf("unexpected replace for moduleA: %#v ok=%v", rep2, ok)
	}
	if _, ok := mod.FindReplace("not/present"); ok {
		t.Fatal("FindReplace found a replace that does not exist")
	}
}

func TestParseModFileSingleLineDirectives(t *testing.T) {
	data := []byte("module example.com/root/moduleA/v2\n\ngo 1.26.0\n\nrequire example.com/root/v2 v2.0.0\n\nreplace example.com/root/v2 => ./..\n")
	mod, err := ParseModFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Require) != 1 || mod.Require[0].Path != "example.com/root/v2" || mod.Require[0].Version != "v2.0.0" {
		t.Fatalf("unexpected single-line require: %#v", mod.Require)
	}
	if len(mod.Replace) != 1 || mod.Replace[0].NewPath != "./.." {
		t.Fatalf("unexpected single-line replace: %#v", mod.Replace)
	}
}

// TestParseModFileAcceptsGodebugDirective proves ParseModFile recognizes
// the go.mod `godebug` directive (single-line and block form), matching
// real go.mod files in this repository (e.g. the root module and several
// contrib modules use `godebug x509negativeserial=1`). Rejecting a real,
// unchanged go.mod as unparseable would make ValidateGeneration fail
// closed on every legitimate release, not just a hostile one.
func TestParseModFileAcceptsGodebugDirective(t *testing.T) {
	data := []byte("module example.com/root\n\ngo 1.26.0\n\ngodebug x509negativeserial=1\n")
	mod, err := ParseModFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Godebug) != 1 || mod.Godebug[0] != "x509negativeserial=1" {
		t.Fatalf("unexpected godebug: %#v", mod.Godebug)
	}

	blockData := []byte("module example.com/root\n\ngo 1.26.0\n\ngodebug (\n\tx509negativeserial=1\n\tasynctimerchan=0\n)\n")
	blockMod, err := ParseModFile(blockData)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockMod.Godebug) != 2 || blockMod.Godebug[0] != "x509negativeserial=1" || blockMod.Godebug[1] != "asynctimerchan=0" {
		t.Fatalf("unexpected block godebug: %#v", blockMod.Godebug)
	}
}

func TestParseModFileVersionQualifiedReplaceIsNotFilesystem(t *testing.T) {
	data := []byte(`module example.com/x

go 1.26.0

replace example.com/y v1.0.0 => example.com/z v1.2.3
`)
	mod, err := ParseModFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Replace) != 1 {
		t.Fatalf("unexpected replace count: %#v", mod.Replace)
	}
	rep := mod.Replace[0]
	if rep.OldVersion != "v1.0.0" || rep.NewPath != "example.com/z" || rep.NewVersion != "v1.2.3" {
		t.Fatalf("unexpected version-qualified replace: %#v", rep)
	}
	if rep.IsFilesystemReplacement() {
		t.Fatal("version-qualified replace incorrectly classified as filesystem replacement")
	}
}

func TestParseModFileRejectsUnrecognizedSyntax(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{name: "block comment", data: "module example.com/x\n\n/* nope */\ngo 1.26.0\n"},
		{name: "unknown directive", data: "module example.com/x\n\nbogus_directive foo=1\n"},
		{name: "missing module", data: "go 1.26.0\n"},
		{name: "duplicate module", data: "module example.com/x\nmodule example.com/y\n"},
		{name: "unterminated block", data: "module example.com/x\n\nrequire (\n\texample.com/y v1.0.0\n"},
		{name: "escaped quoted path", data: "module example.com/x\n\nreplace example.com/y => \"esc\\\\aped\"\n"},
		{name: "malformed require", data: "module example.com/x\n\nrequire example.com/y\n"},
		{name: "malformed replace", data: "module example.com/x\n\nreplace example.com/y\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseModFile([]byte(tc.data)); ErrorCode(err) == "" {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

func TestParseModFileDetectsDuplicateReplace(t *testing.T) {
	data := []byte(`module example.com/x

replace example.com/y => ./a
replace example.com/y => ./b
`)
	mod, err := ParseModFile(data)
	if err != nil {
		t.Fatal(err)
	}
	duplicates := mod.DuplicateReplacePaths()
	if len(duplicates) != 1 || duplicates[0] != "example.com/y" {
		t.Fatalf("unexpected duplicates: %#v", duplicates)
	}
}

func TestIsFilesystemReplacementRecognizesWindowsVolumePath(t *testing.T) {
	rep := ModFileReplace{NewPath: `C:\repo\module`}
	if !rep.IsFilesystemReplacement() {
		t.Fatal("windows volume path not recognized as filesystem replacement")
	}
}
