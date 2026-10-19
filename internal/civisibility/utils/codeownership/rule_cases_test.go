// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package codeownership

import (
	"slices"
	"strings"
	"testing"
)

type fileTest struct {
	path   string
	owners []string
}

type ruleTest struct {
	name, rules string
	diagnostics int
	files       []fileTest
}

func checkRules(t *testing.T, dialect Dialect, cases []ruleTest) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse(strings.NewReader(tc.rules), dialect)
			if err != nil {
				t.Fatal(err)
			}
			if c.Diagnostics() != tc.diagnostics {
				t.Fatalf("diagnostics=%d want=%d", c.Diagnostics(), tc.diagnostics)
			}
			for _, file := range tc.files {
				got, _ := c.Match(file.path)
				if !slices.Equal(got.Owners(), file.owners) {
					t.Errorf("path=%q owners=%v want=%v", file.path, got.Owners(), file.owners)
				}
			}
		})
	}
}
