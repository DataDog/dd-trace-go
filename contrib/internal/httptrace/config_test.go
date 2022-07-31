// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	defaultCfg := config{
		clientIP:          true,
		queryString:       true,
		queryStringRegexp: defaultQueryStringRegexp,
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
				envQueryStringDisabled:    "invalid",
				envClientIPHeaderDisabled: "invalid",
				envQueryStringRegexp:      "+",
			},
			cfg: defaultCfg,
		},
		{
			name: "disable-query",
			env:  map[string]string{envQueryStringDisabled: "true"},
			cfg: config{
				clientIP:          true,
				queryStringRegexp: defaultQueryStringRegexp,
			},
		},
		{
			name: "disable-ip",
			env:  map[string]string{envClientIPHeaderDisabled: "true"},
			cfg: config{
				queryString:       true,
				queryStringRegexp: defaultQueryStringRegexp,
			},
		},
		{
			name: "disable-query-obf",
			env:  map[string]string{envQueryStringRegexp: ""},
			cfg: config{
				queryString: true,
				clientIP:    true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer cleanEnv()()
			for k, v := range tc.env {
				os.Setenv(k, v)
			}
			c := newConfig()
			require.Equal(t, tc.cfg.queryStringRegexp, c.queryStringRegexp)
			require.Equal(t, tc.cfg.queryString, c.queryString)
			require.Equal(t, tc.cfg.clientIPHeader, c.clientIPHeader)
			require.Equal(t, tc.cfg.clientIP, c.clientIP)
		})
	}
}

func cleanEnv() func() {
	env := map[string]string{
		envQueryStringDisabled:    os.Getenv(envQueryStringDisabled),
		envQueryStringRegexp:      os.Getenv(envQueryStringRegexp),
		envClientIPHeaderDisabled: os.Getenv(envClientIPHeaderDisabled),
		envClientIPHeader:         os.Getenv(envClientIPHeader),
	}
	for k := range env {
		os.Unsetenv(k)
	}
	return func() {
		for k, v := range env {
			os.Setenv(k, v)
		}
	}
}
