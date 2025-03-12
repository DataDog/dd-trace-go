// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"reflect"
	"testing"
)

func TestRemoveEmptyStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "All non-empty strings",
			input: []string{"hello", "world"},
			want:  []string{"hello", "world"},
		},
		{
			name:  "All empty strings",
			input: []string{"", "", ""},
			want:  []string{},
		},
		{
			name:  "Mixed empty and non-empty strings",
			input: []string{"one", "", "two", "", "three"},
			want:  []string{"one", "two", "three"},
		},
		{
			name:  "Empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "Empty string at the beginning",
			input: []string{"", "start", "end"},
			want:  []string{"start", "end"},
		},
		{
			name:  "Empty string at the end",
			input: []string{"start", "end", ""},
			want:  []string{"start", "end"},
		},
		{
			name:  "Multiple consecutive empty strings",
			input: []string{"start", "", "", "end", ""},
			want:  []string{"start", "end"},
		},
		{
			name:  "Strings with spaces (not considered empty)",
			input: []string{" ", "text", "", "  "},
			want:  []string{" ", "text", "  "},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removeEmptyStrings(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("removeEmptyStrings(%v) = %v; expected %v", tc.input, got, tc.want)
			}
		})
	}
}
