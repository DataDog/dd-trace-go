// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const selectSuitesScript = "../../.github/workflows/apps/select-orchestrion-suites.sh"

// integrationPkgBase is the orchestrion integration module's package path.
const integrationPkgBase = "github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration"

// integrationPackages mirrors the orchestrion lane's platform-aware input:
// the _integration root package, a few suites, and the internal helper
// package. grep -v /civisibility already excluded the civisibility and
// civisibility_failnow* packages, exactly as the Run Tests step feeds it.
var integrationPackages = strings.Join([]string{
	integrationPkgBase,
	integrationPkgBase + "/gin",
	integrationPkgBase + "/chi.v5",
	integrationPkgBase + "/internal/maintest",
}, "\n") + "\n"

// integrationPackagesLine is the space-joined form of the input, which is
// what the script prints for a full-lane run.
var integrationPackagesLine = strings.Join([]string{
	integrationPkgBase,
	integrationPkgBase + "/gin",
	integrationPkgBase + "/chi.v5",
	integrationPkgBase + "/internal/maintest",
}, " ")

// runSelectSuites executes the selection script the way the Run Tests step
// does: SUITES in the environment, the package list on stdin.
func runSelectSuites(
	t *testing.T, suites string,
) (stdout, stderr string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not found: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), "bash", selectSuitesScript)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "SUITES=" + suites}
	cmd.Stdin = strings.NewReader(integrationPackages)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			selectSuitesScript, err, out.String(), errBuf.String())
	}
	return out.String(), errBuf.String()
}

// splitSelection splits the script's two output lines: the gotestsum package
// list and the civisibility target.
func splitSelection(t *testing.T, stdout string) (packages, civisibility string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("selection script printed %d lines, want 2:\n%s", len(lines), stdout)
	}
	return lines[0], lines[1]
}

// TestSelectOrchestrionSuites pins the orchestrion lane's narrowing: the
// full-lane tokens pass through, a narrowed run keeps the module's own
// packages, and an empty match falls back to the full lane with a warning.
func TestSelectOrchestrionSuites(t *testing.T) {
	base := integrationPkgBase
	// The module's own packages, in input order: the root package holds
	// TestOrchestrionPresent, internal/ holds the shared helpers.
	own := base + " " + base + "/internal/maintest"

	tests := []struct {
		name         string
		suites       string
		wantPackages string
		wantCiv      string
		wantWarning  bool
	}{
		{
			name:         "ALL passes the full list through",
			suites:       "ALL",
			wantPackages: integrationPackagesLine,
			wantCiv:      "./civisibility",
		},
		{
			name:         "NONE passes the full list through",
			suites:       "NONE",
			wantPackages: integrationPackagesLine,
			wantCiv:      "./civisibility",
		},
		{
			name:         "an empty selection passes the full list through",
			suites:       "",
			wantPackages: integrationPackagesLine,
			wantCiv:      "./civisibility",
		},
		{
			name:         "a single suite keeps the module's own packages",
			suites:       "gin",
			wantPackages: base + " " + base + "/gin " + base + "/internal/maintest",
			wantCiv:      "",
		},
		{
			name:   "a multi-suite selection keeps every named suite",
			suites: "gin chi.v5",
			wantPackages: base + " " + base + "/gin " +
				base + "/chi.v5 " + base + "/internal/maintest",
			wantCiv: "",
		},
		{
			// The dot in chi.v5 is matched literally.
			name:         "a versioned suite name matches exactly",
			suites:       "chi.v5",
			wantPackages: base + " " + base + "/chi.v5 " + base + "/internal/maintest",
			wantCiv:      "",
		},
		{
			// civisibility runs separately; the gotestsum list keeps only
			// the module's own packages.
			name:         "a civisibility-only selection runs nothing extra",
			suites:       "civisibility",
			wantPackages: own,
			wantCiv:      "./civisibility",
		},
		{
			// Trailing whitespace in the classifier output must not break
			// the token comparison.
			name:         "surrounding whitespace is trimmed",
			suites:       "  gin  ",
			wantPackages: base + " " + base + "/gin " + base + "/internal/maintest",
			wantCiv:      "",
		},
		{
			// civisibility_failnow is excluded from the input list, so the
			// selection matches nothing: fall back to the full lane rather
			// than report a green no-op. The package list pins the fallback;
			// the warning is what this row adds.
			name:         "an unmatched selection falls back to the full lane",
			suites:       "civisibility_failnow",
			wantPackages: integrationPackagesLine,
			wantCiv:      "./civisibility",
			wantWarning:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := runSelectSuites(t, tt.suites)
			packages, civ := splitSelection(t, stdout)
			if packages != tt.wantPackages {
				t.Errorf("package list = %q, want %q", packages, tt.wantPackages)
			}
			if civ != tt.wantCiv {
				t.Errorf("civisibility target = %q, want %q", civ, tt.wantCiv)
			}
			if tt.wantWarning && !strings.Contains(stderr, "::warning::") {
				t.Errorf("stderr misses the fallback warning: %q", stderr)
			}
			if !tt.wantWarning && stderr != "" {
				t.Errorf("stderr holds an unexpected warning: %q", stderr)
			}
		})
	}
}
