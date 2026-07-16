// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"strings"
	"testing"
)

func TestAgentNameWireSafe(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain-safe", "my_agent", true},
		{"empty", "", true},
		{"contains-comma", "my,agent", false},
		{"contains-equals-is-safe", "key=val", true},
		{"space-is-safe", "my agent", true},
		{"tilde-boundary-safe", "~", true},
		{"space-boundary-safe", " ", true},
		{"tab-non-printable", "my\tagent", false},
		{"newline-non-printable", "my\nagent", false},
		{"multibyte-utf8-unsafe", "agént", false},
		{"len-256-safe", strings.Repeat("a", 256), true},
		{"len-257-unsafe", strings.Repeat("a", 257), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentNameWireSafe(tc.input); got != tc.want {
				t.Fatalf("agentNameWireSafe(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
