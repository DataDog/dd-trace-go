// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitHostPort(t *testing.T) {
	for _, tc := range []struct {
		Addr                       string
		ExpectedHost, ExpectedPort string
	}{
		{
			Addr:         "",
			ExpectedHost: "",
			ExpectedPort: "",
		},
		{
			Addr:         ":33",
			ExpectedPort: "33",
		},
		{
			Addr:         "33:",
			ExpectedHost: "33",
		},
		{
			Addr:         "[fe80::1.2.3.4]:33",
			ExpectedHost: "fe80::1.2.3.4",
			ExpectedPort: "33",
		},
		{
			Addr:         "[fe80::1.2.3.4]",
			ExpectedHost: "fe80::1.2.3.4",
		},
		{
			Addr:         " [fe80::1.2.3.4] ",
			ExpectedHost: "fe80::1.2.3.4",
		},
		{
			Addr:         "localhost:80 ",
			ExpectedHost: "localhost",
			ExpectedPort: "80",
		},
	} {
		t.Run(tc.Addr, func(t *testing.T) {
			host, port := SplitHostPort(tc.Addr)
			require.Equal(t, tc.ExpectedHost, host)
			require.Equal(t, tc.ExpectedPort, port)
		})
	}
}

func TestMakeHTTPHeaders(t *testing.T) {
	for _, tc := range []struct {
		headers  map[string][]string
		expected map[string]string
	}{
		{
			headers:  nil,
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie": {"not-collected"},
			},
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8",
			},
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8", "9.10.11.12,13.14.15.16"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8;9.10.11.12,13.14.15.16",
			},
		},
	} {
		t.Run("MakeHTTPHeaders", func(t *testing.T) {
			headers := MakeHTTPHeaders(tc.headers)
			require.Equal(t, tc.expected, headers)
		})
	}
}
