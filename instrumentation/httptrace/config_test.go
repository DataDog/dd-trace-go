// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	defaultCfg := config{
		queryString:          true,
		useDefaultObfuscator: true,
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		cfg  config // cfg is the expected output config
	}{
		{
			name: "empty-env",
			cfg:  defaultCfg,
		},
		{
			name: "bad-values",
			env: map[string]string{
				envQueryStringDisabled: "invalid",
				EnvQueryStringRegexp:   "+",
			},
			// Fail closed: an invalid regexp drops the query string.
			cfg: config{
				queryString:     true,
				dropQueryString: true,
			},
		},
		{
			name: "explicit-default-regexp",
			env:  map[string]string{EnvQueryStringRegexp: defaultQueryStringPattern},
			cfg:  defaultCfg,
		},
		{
			name: "explicit-default-regexp-non-capturing",
			env:  map[string]string{EnvQueryStringRegexp: defaultQueryStringPatternNonCapturing},
			cfg:  defaultCfg,
		},
		{
			name: "disable-query",
			env:  map[string]string{envQueryStringDisabled: "true"},
			cfg: config{
				useDefaultObfuscator: true,
			},
		},
		{
			name: "disable-query-obf",
			env:  map[string]string{EnvQueryStringRegexp: ""},
			cfg: config{
				queryString: true,
			},
		},
		{
			name: "custom-regexp",
			env:  map[string]string{EnvQueryStringRegexp: "secret=[^&]+"},
			cfg: config{
				queryString:       true,
				queryStringRegexp: regexp.MustCompile("secret=[^&]+"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c := newConfig()
			require.Equal(t, tc.cfg.queryStringRegexp, c.queryStringRegexp)
			require.Equal(t, tc.cfg.useDefaultObfuscator, c.useDefaultObfuscator)
			require.Equal(t, tc.cfg.dropQueryString, c.dropQueryString)
			require.Equal(t, tc.cfg.queryString, c.queryString)
		})
	}
}

func TestDefaultQueryStringPattern(t *testing.T) {
	// The non-capturing form must differ from the capturing form only by
	// the JWT delimiter group, and must have no capture group.
	require.NotEqual(t, defaultQueryStringPattern, defaultQueryStringPatternNonCapturing)
	require.Equal(t, 1, defaultQueryStringRegexp.NumSubexp())
	nonCapturing := regexp.MustCompile(defaultQueryStringPatternNonCapturing)
	require.Equal(t, 0, nonCapturing.NumSubexp())
	require.Equal(t, strings.Replace(defaultQueryStringPatternNonCapturing, "(?:^|", "(^|", 1), defaultQueryStringPattern)
}
