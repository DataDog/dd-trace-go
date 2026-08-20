// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"testing"

	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestConfigOTelSemantics(t *testing.T) {
	t.Cleanup(func() { internalconfig.CreateNew() })
	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "")
	internalconfig.CreateNew()
	require.False(t, newConfig().otelSemanticsEnabled)

	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")
	internalconfig.CreateNew()
	c := newConfig()
	require.True(t, c.otelSemanticsEnabled)

	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "false")
	internalconfig.CreateNew()
	require.True(t, c.otelSemanticsEnabled, "configuration must capture the effective mode")
	require.False(t, newConfig().otelSemanticsEnabled)
}

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
			cfg: config{
				queryString:       true,
				queryStringRegexp: defaultQueryStringRegexp,
			},
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c := newConfig()
			require.Equal(t, tc.cfg.queryStringRegexp, c.queryStringRegexp)
			require.Equal(t, tc.cfg.useDefaultObfuscator, c.useDefaultObfuscator)
			require.Equal(t, tc.cfg.queryString, c.queryString)
		})
	}
}
