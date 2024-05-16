// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package actions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewBlockParams(t *testing.T) {
	for name, tc := range map[string]struct {
		params   map[string]any
		expected BlockActionParams
	}{
		"block-1": {
			params: map[string]any{
				"status_code": "403",
				"type":        "auto",
			},
			expected: BlockActionParams{
				Type:       "auto",
				StatusCode: 403,
			},
		},
		"block-2": {
			params: map[string]any{
				"status_code": "405",
				"type":        "html",
			},
			expected: BlockActionParams{
				Type:       "html",
				StatusCode: 405,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			actionParams, err := BlockParamsFromMap(tc.params)
			require.NoError(t, err)
			require.Equal(t, tc.expected.Type, actionParams.Type)
			require.Equal(t, tc.expected.StatusCode, actionParams.StatusCode)
		})
	}
}

func TestNewRedirectParams(t *testing.T) {
	for name, tc := range map[string]struct {
		params   map[string]any
		expected RedirectActionParams
	}{
		"redirect-1": {
			params: map[string]any{
				"status_code": "308",
				"location":    "/redirected",
			},
			expected: RedirectActionParams{
				Location:   "/redirected",
				StatusCode: 308,
			},
		},
		"redirect-2": {
			params: map[string]any{
				"status_code": "303",
				"location":    "/tmp",
			},
			expected: RedirectActionParams{
				Location:   "/tmp",
				StatusCode: 303,
			},
		},
		"no-location": {
			params: map[string]any{
				"status_code": "303",
			},
			expected: RedirectActionParams{
				Location:   "",
				StatusCode: 303,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			actionParams, err := RedirectParamsFromMap(tc.params)
			require.NoError(t, err)
			require.Equal(t, tc.expected, actionParams)
		})
	}
}
