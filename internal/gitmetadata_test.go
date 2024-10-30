// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package internal

import (
	"runtime/debug"
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

func TestGetTagsFromBinary(t *testing.T) {
	testCases := []struct {
		name     string
		in       string
		expected map[string]string
	}{
		{
			name:     "empty build info",
			expected: map[string]string{},
		},
		{
			name: "build info with module path",
			expected: map[string]string{
				TagGoPath: "github.com/DataDog/dd-trace-go",
			},
		},
		{
			name: "build info with module path and git repository",
			expected: map[string]string{
				TagGoPath:    "github.com/DataDog/dd-trace-go",
				TagCommitSha: "123456",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			readBuildInfo := func() (*debug.BuildInfo, bool) {
				info := &debug.BuildInfo{
					Settings: []debug.BuildSetting{
						{
							Key:   "vcs",
							Value: "git",
						},
					},
				}
				if tc.expected[TagGoPath] != "" {
					info.Path = tc.expected[TagGoPath]
				}
				if tc.expected[TagCommitSha] != "" {
					info.Settings = append(info.Settings, debug.BuildSetting{
						Key:   "vcs.revision",
						Value: tc.expected[TagCommitSha],
					})
				}
				return info, true
			}
			tags := getTagsFromBinary(readBuildInfo)
			assert.Subset(t, tags, tc.expected)
		})
	}
}

func BenchmarkGetGitMetadataTags(b *testing.B) {
	b.Setenv(EnvGitMetadataEnabledFlag, "true")
	for i := 0; i < b.N; i++ {
		GetGitMetadataTags()
	}
}
