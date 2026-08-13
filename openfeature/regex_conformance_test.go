// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTargetingRegexConformance(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(
		"ffe-system-test-data",
		"regex-conformance",
		"targeting-regex-conformance.json",
	))
	if err != nil {
		t.Fatalf("read targeting regex conformance fixture: %v (initialize it with `git submodule update --init --recursive`)", err)
	}

	var fixture struct {
		Schema          string `json:"schema"`
		SchemaVersion   int    `json:"schemaVersion"`
		ContractVersion string `json:"contractVersion"`
		Cases           []struct {
			ID                 string `json:"id"`
			Contract           string `json:"contract"`
			NormalizedPattern  string `json:"normalizedPattern"`
			Input              string `json:"input"`
			ExpectedCompile    *bool  `json:"expectedCompile"`
			ExpectedMatch      *bool  `json:"expectedMatch"`
			EngineExpectations struct {
				Go *struct {
					Compile bool  `json:"compile"`
					Match   *bool `json:"match"`
				} `json:"go"`
			} `json:"engineExpectations"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse targeting regex conformance fixture: %v", err)
	}
	if fixture.Schema != "datadog.ffe.targeting-regex-conformance/v1" ||
		fixture.SchemaVersion != 1 || fixture.ContractVersion != "targeting-regex-v2" {
		t.Fatalf("unsupported targeting regex conformance fixture: schema=%q schemaVersion=%d contractVersion=%q",
			fixture.Schema, fixture.SchemaVersion, fixture.ContractVersion)
	}
	if len(fixture.Cases) != 75 {
		t.Fatalf("targeting regex conformance fixture has %d cases, want 75", len(fixture.Cases))
	}

	seenIDs := make(map[string]struct{}, len(fixture.Cases))
	for _, tc := range fixture.Cases {
		if tc.ID == "" {
			t.Fatal("targeting regex conformance fixture has a case with an empty id")
		}
		if _, exists := seenIDs[tc.ID]; exists {
			t.Fatalf("targeting regex conformance fixture has duplicate id %q", tc.ID)
		}
		seenIDs[tc.ID] = struct{}{}

		t.Run(tc.ID, func(t *testing.T) {
			wantCompile := tc.ExpectedCompile
			wantMatch := tc.ExpectedMatch
			if tc.EngineExpectations.Go != nil {
				wantCompile = &tc.EngineExpectations.Go.Compile
				wantMatch = tc.EngineExpectations.Go.Match
			}
			if wantCompile == nil {
				t.Fatalf("fixture has no Go compile expectation for %s pattern", tc.Contract)
			}

			_, err := loadRegex(tc.NormalizedPattern)
			if got := err == nil; got != *wantCompile {
				t.Fatalf("compile normalized pattern %q: got %t, want %t (error: %v)", tc.NormalizedPattern, got, *wantCompile, err)
			}

			if wantMatch == nil {
				return
			}
			if got := matchesRegex(tc.Input, tc.NormalizedPattern); got != *wantMatch {
				t.Errorf("match normalized pattern %q against %q: got %t, want %t", tc.NormalizedPattern, tc.Input, got, *wantMatch)
			}
		})
	}
}
