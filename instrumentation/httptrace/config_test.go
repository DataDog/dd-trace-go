// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	defaultCfg := config{
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
				envQueryStringDisabled: "invalid",
				envQueryStringRegexp:   "+",
			},
			cfg: defaultCfg,
		},
		{
			name: "disable-query",
			env:  map[string]string{envQueryStringDisabled: "true"},
			cfg: config{
				queryStringRegexp: defaultQueryStringRegexp,
			},
		},
		{
			name: "disable-query-obf",
			env:  map[string]string{envQueryStringRegexp: ""},
			cfg: config{
				queryString: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c := newConfig()
			require.Equal(t, tc.cfg.queryStringRegexp, c.queryStringRegexp)
			require.Equal(t, tc.cfg.queryString, c.queryString)
		})
	}
}
