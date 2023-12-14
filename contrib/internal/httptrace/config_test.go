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
		traceClientIP:     false,
		setHTTPError:      true,
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
				queryString:       false,
				queryStringRegexp: defaultQueryStringRegexp,
				traceClientIP:     false,
				setHTTPError:      true,
			},
		},
		{
			name: "disable-query-obf",
			env:  map[string]string{envQueryStringRegexp: ""},
			cfg: config{
				queryString:       true,
				queryStringRegexp: nil,
				traceClientIP:     false,
				setHTTPError:      true,
			},
		},
		{
			name: "disable-set-http-error",
			env:  map[string]string{envSetHTTPErrorDisabled: "true"},
			cfg: config{
				queryString:       true,
				queryStringRegexp: defaultQueryStringRegexp,
				traceClientIP:     false,
				setHTTPError:      false,
			},
		},
		{
			name: "disable-set-http-error-obf",
			env:  map[string]string{envSetHTTPErrorDisabled: ""},
			cfg:  defaultCfg,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c := newConfig()
			require.Equal(t, tc.cfg, c)
		})
	}
}
