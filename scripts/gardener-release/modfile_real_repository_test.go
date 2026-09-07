// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseModFileAgainstRealRepository is a regression test (added
// during B07 review, not part of the original test matrix) that parses
// every real go.mod file in this repository to confirm ParseModFile's
// minimal grammar subset does not choke on real-world syntax it will
// actually see in production. This caught a real bug: the parser
// originally rejected the `godebug` directive used by the root module and
// several contrib modules, which would have made ValidateGeneration fail
// closed on every legitimate release touching those go.mod files, not
// just a hostile one. Kept permanently as ground-truth coverage beyond
// synthetic fixtures.
func TestParseModFileAgainstRealRepository(t *testing.T) {
	root := "../.."
	var checked int
	var failures []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() != "go.mod" {
			return nil
		}
		if strings.Contains(path, "testdata") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		if _, parseErr := ParseModFile(data); parseErr != nil {
			failures = append(failures, path+": "+parseErr.Error())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("expected to check many real go.mod files, only checked %d", checked)
	}
	if len(failures) > 0 {
		t.Fatalf("ParseModFile failed on %d real go.mod files:\n%s", len(failures), strings.Join(failures, "\n"))
	}
	t.Logf("ParseModFile succeeded on %d real go.mod files", checked)
}
