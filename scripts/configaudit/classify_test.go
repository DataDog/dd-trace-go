package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	known := map[string]struct{}{
		"DD_AGENT_HOST": {},
		"DD_SERVICE":    {},
		"DD_SITE":       {},
	}
	migrated := map[string]struct{}{
		"DD_AGENT_HOST": {},
		"DD_SERVICE":    {},
	}
	reads := map[string][]CallSite{
		"DD_SITE":       {{File: "x.go", Line: 1, Func: "env.Get"}},
		"DD_AGENT_HOST": {{File: "y.go", Line: 2, Func: "env.Get"}}, // also still read outside (legacy)
		"DD_UNKNOWN":    {{File: "z.go", Line: 3, Func: "env.Get"}},
	}
	res := classify(known, migrated, reads)

	migratedKeys := keySet(res.Migrated)
	unmigratedKeys := keySet(res.Unmigrated)
	untrackedKeys := keySet(res.Untracked)
	stillReadKeys := keySet(res.MigratedButStillReadOutside)

	wantEq(t, "migrated", migratedKeys, []string{"DD_AGENT_HOST", "DD_SERVICE"})
	wantEq(t, "unmigrated", unmigratedKeys, []string{"DD_SITE"})
	wantEq(t, "untracked", untrackedKeys, []string{"DD_UNKNOWN"})
	wantEq(t, "stillReadOutside", stillReadKeys, []string{"DD_AGENT_HOST"})
}

func TestRenderTable(t *testing.T) {
	res := AuditResult{
		Unmigrated: []ConfigEntry{
			{Name: "DD_SITE", CallSites: []CallSite{{File: "a.go", Line: 1}}},
		},
	}
	var buf bytes.Buffer
	if err := renderTable(&buf, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "DD_SITE") {
		t.Fatalf("expected DD_SITE in table output, got %q", buf.String())
	}
}

func TestRenderJSON(t *testing.T) {
	res := AuditResult{
		Unmigrated: []ConfigEntry{{Name: "DD_SITE"}},
	}
	var buf bytes.Buffer
	if err := renderJSON(&buf, res); err != nil {
		t.Fatal(err)
	}
	var got AuditResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Unmigrated) != 1 || got.Unmigrated[0].Name != "DD_SITE" {
		t.Fatalf("round-trip failed: %+v", got)
	}
}

func keySet(entries []ConfigEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

func wantEq(t *testing.T, label string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}
