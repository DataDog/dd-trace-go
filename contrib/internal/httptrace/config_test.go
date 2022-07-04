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
	restore := cleanEnv()
	defaultCfg := newConfig()
	restore()
	for _, tc := range []struct {
		name      string
		env       map[string]string
		cfgSetter func(*config)
	}{
		{
			name: "empty-env",
		},
		{
			name: "bad-values",
			env: map[string]string{
				envQueryStringDisabled:    "invalid",
				envClientIPHeaderDisabled: "invalid",
				envQueryStringRegexp:      "+",
			},
		},
		{
			name: "disable-query",
			env:  map[string]string{envQueryStringDisabled: "true"},
			cfgSetter: func(c *config) {
				c.collectQueryString = false
			},
		},
		{
			name: "disable-ip",
			env:  map[string]string{envClientIPHeaderDisabled: "true"},
			cfgSetter: func(c *config) {
				c.collectIP = false
			},
		},
		{
			name: "disable-query-obf",
			env:  map[string]string{envQueryStringRegexp: ""},
			cfgSetter: func(c *config) {
				c.queryStringObfRegexp = nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer cleanEnv()()
			for k, v := range tc.env {
				os.Setenv(k, v)
			}
			expectedCfg := defaultCfg
			if tc.cfgSetter != nil {
				tc.cfgSetter(&expectedCfg)
			}
			c := newConfig()
			require.Equal(t, expectedCfg.queryStringObfRegexp, c.queryStringObfRegexp)
			require.Equal(t, expectedCfg.collectQueryString, c.collectQueryString)
			require.Equal(t, expectedCfg.clientIPHeader, c.clientIPHeader)
			require.Equal(t, expectedCfg.collectIP, c.collectIP)
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
