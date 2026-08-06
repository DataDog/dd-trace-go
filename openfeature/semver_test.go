// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSemver(t *testing.T) {
	valid := []struct {
		version string
		want    parsedSemver
	}{
		{version: "0.0.0", want: parsedSemver{}},
		{
			version: "18446744073709551615.18446744073709551615.18446744073709551615",
			want: parsedSemver{
				major: ^uint64(0),
				minor: ^uint64(0),
				patch: ^uint64(0),
			},
		},
		{version: "1.2.3-alpha.1", want: parsedSemver{major: 1, minor: 2, patch: 3, prerelease: "alpha.1"}},
		{
			version: "1.2.3-18446744073709551616",
			want:    parsedSemver{major: 1, minor: 2, patch: 3, prerelease: "18446744073709551616"},
		},
		{version: "1.2.3+build.001", want: parsedSemver{major: 1, minor: 2, patch: 3}},
		{
			version: "1.2.3-alpha-1+build.001",
			want:    parsedSemver{major: 1, minor: 2, patch: 3, prerelease: "alpha-1"},
		},
	}
	for _, tt := range valid {
		t.Run("valid/"+tt.version, func(t *testing.T) {
			got, ok := parseSemver(tt.version)
			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}

	invalid := []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		"v1.2.3",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"18446744073709551616.0.0",
		"0.18446744073709551616.0",
		"0.0.18446744073709551616",
		"1.2.3-",
		"1.2.3+",
		"1.2.3-alpha..1",
		"1.2.3+build..1",
		"1.2.3-01",
		"1.2.3-alpha_1",
		"1.2.3-alpha+build+other",
		"1.2.3-α",
		" 1.2.3",
		"1.2.3 ",
	}
	for _, version := range invalid {
		t.Run("invalid/"+version, func(t *testing.T) {
			_, ok := parseSemver(version)
			require.False(t, ok)
		})
	}
}

func TestCompareSemver(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for i := range ordered {
		left, ok := parseSemver(ordered[i])
		require.True(t, ok)
		for j := range ordered {
			right, ok := parseSemver(ordered[j])
			require.True(t, ok)
			ordering := compareSemver(left, right)
			switch {
			case i < j:
				require.Negative(t, ordering, "%q should precede %q", ordered[i], ordered[j])
			case i > j:
				require.Positive(t, ordering, "%q should follow %q", ordered[i], ordered[j])
			default:
				require.Zero(t, ordering)
			}
		}
	}

	t.Run("arbitrarily large numeric prerelease identifiers", func(t *testing.T) {
		left, ok := parseSemver("1.0.0-99999999999999999999")
		require.True(t, ok)
		right, ok := parseSemver("1.0.0-100000000000000000000")
		require.True(t, ok)
		require.Negative(t, compareSemver(left, right))
	})

	t.Run("build metadata is ignored", func(t *testing.T) {
		left, ok := parseSemver("1.0.0+build.1")
		require.True(t, ok)
		right, ok := parseSemver("1.0.0+build.2")
		require.True(t, ok)
		require.Zero(t, compareSemver(left, right))
	})
}
