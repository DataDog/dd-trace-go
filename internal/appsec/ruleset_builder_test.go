// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package appsec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidation(t *testing.T) {

	for _, tc := range []struct {
		name  string
		f     rulesetFragment
		valid bool
	}{
		{
			name:  "empty",
			valid: true,
		},
		{
			name: "overrides/empty",
			f: rulesetFragment{
				Overrides: []rulesOverrideEntry{{}},
			},
		},
		{
			name: "overrides/valid",
			f: rulesetFragment{
				Overrides: []rulesOverrideEntry{
					{
						ID: "rule-id",
					},
					{
						RulesTarget: []interface{}{nil, nil, nil},
					},
					{
						ID:          "rule-id",
						RulesTarget: []interface{}{nil, nil, nil},
					},
					{
						ID:          "rule-id",
						RulesTarget: []interface{}{nil, nil, nil},
						Enabled:     false,
					},
					{
						ID:          "rule-id",
						RulesTarget: []interface{}{nil, nil, nil},
						Enabled:     false,
					},
					{
						ID:          "rule-id",
						RulesTarget: []interface{}{nil, nil, nil},
						Enabled:     false,
						OnMatch:     []interface{}{nil, nil, nil},
					},
				},
			},
			valid: true,
		},
		{
			name: "overrides/invalid",
			f: rulesetFragment{
				Overrides: []rulesOverrideEntry{
					{
						Enabled: false,
					},
				},
			},
		},
		{
			name: "overrides/invalid",
			f: rulesetFragment{
				Overrides: []rulesOverrideEntry{
					{
						Enabled: true,
					},
				},
			},
		},
		{
			name: "overrides/invalid",
			f: rulesetFragment{
				Overrides: []rulesOverrideEntry{
					{
						OnMatch: []interface{}{nil, nil, nil},
					},
				},
			},
		},
		{
			name: "overrides/invalid",
			f: rulesetFragment{
				Overrides: []rulesOverrideEntry{
					{
						Enabled: false,
						OnMatch: []interface{}{nil, nil, nil},
					},
				},
			},
		},
		{
			name: "exclusions/empty",
			f: rulesetFragment{
				Exclusions: []exclusionEntry{{}},
			},
		},
		{
			name: "exclusions/valid",
			f: rulesetFragment{
				Exclusions: []exclusionEntry{
					{
						ID:         "filter-id",
						Conditions: []interface{}{nil, nil, nil},
					},
					{
						ID:     "filter-id",
						Inputs: []interface{}{nil, nil, nil},
					},
					{
						ID:          "filter-id",
						RulesTarget: []interface{}{nil, nil, nil},
					},
					{
						ID:         "filter-id",
						Conditions: []interface{}{nil, nil, nil},
						Inputs:     []interface{}{nil, nil, nil},
					},
					{
						ID:          "filter-id",
						Conditions:  []interface{}{nil, nil, nil},
						RulesTarget: []interface{}{nil, nil, nil},
					},
					{
						ID:          "filter-id",
						Inputs:      []interface{}{nil, nil, nil},
						RulesTarget: []interface{}{nil, nil, nil},
					},
				},
			},
			valid: true,
		},
		{
			name: "exclusions/invalid",
			f: rulesetFragment{
				Exclusions: []exclusionEntry{{
					ID: "filter-id",
				}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.valid, tc.f.validate())
		})
	}
}
