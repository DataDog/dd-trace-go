// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestLoadKnown(t *testing.T) {
	// Use the real supported_configurations.json so the test stays honest.
	path := filepath.Join("..", "..", "internal", "env", "supported_configurations.json")
	got, err := loadKnown(path)
	if err != nil {
		t.Fatalf("loadKnown: %v", err)
	}
	if len(got) < 100 {
		t.Fatalf("expected at least 100 known DD_* configs, got %d", len(got))
	}
	// Spot-check a few known entries.
	for _, key := range []string{"DD_SERVICE", "DD_AGENT_HOST", "DD_TRACE_STARTUP_LOGS"} {
		if _, ok := got[key]; !ok {
			t.Errorf("expected %s in known set", key)
		}
	}
}

func TestLoadKnown_AliasIncluded(t *testing.T) {
	// DD_API_KEY has alias DD-API-KEY in the JSON; aliases should appear too.
	path := filepath.Join("..", "..", "internal", "env", "supported_configurations.json")
	got, err := loadKnown(path)
	if err != nil {
		t.Fatalf("loadKnown: %v", err)
	}
	if _, ok := got["DD-API-KEY"]; !ok {
		// list a few keys to aid debugging if this fails
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("expected DD-API-KEY alias in known set; sample: %v", keys[:5])
	}
}

func TestLoadKnown_MissingFile(t *testing.T) {
	_, err := loadKnown("does-not-exist.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadKnown_ShapeRoundTrip(t *testing.T) {
	// Verify the parser doesn't silently drop the impl-letter shape.
	want := []string{"DD_FOO", "DD-FOO-ALIAS"}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "sc.json")
	if err := writeFile(p, []byte(`{"version":"2","supportedConfigurations":{"DD_FOO":[{"implementation":"A","type":"string","default":null,"aliases":["DD-FOO-ALIAS"]}]}}`)); err != nil {
		t.Fatal(err)
	}
	got, err := loadKnown(p)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.Strings(want)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %v, want %v", keys, want)
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
