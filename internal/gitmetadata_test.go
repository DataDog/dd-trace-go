// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoveCredentials(t *testing.T) {
	testCases := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "empty url",
			in:       "",
			expected: "",
		},
		{
			name:     "https url without credential",
			in:       "https://github.com/DataDog/dd-trace-go",
			expected: "https://github.com/DataDog/dd-trace-go",
		},
		{
			name:     "ssh url without credential",
			in:       "git@github.com:DataDog/dd-trace-go.git",
			expected: "git@github.com:DataDog/dd-trace-go.git",
		},
		{
			name:     "https url with user",
			in:       "https://token@github.com/DataDog/dd-trace-go",
			expected: "https://github.com/DataDog/dd-trace-go",
		},
		{
			name:     "https url with user and password",
			in:       "https://user:password@github.com/DataDog/dd-trace-go",
			expected: "https://github.com/DataDog/dd-trace-go",
		},
		{
			name:     "invalid url without scheme",
			in:       "github.com/DataDog/dd-trace-go",
			expected: "github.com/DataDog/dd-trace-go",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, removeCredentials(tc.in))
		})
	}
}
