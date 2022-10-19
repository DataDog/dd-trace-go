// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package httptrace

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
)

func TestConfig(t *testing.T) {
	defaultCfg := config{
		clientIP:          false,
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
				queryStringRegexp: defaultQueryStringRegexp,
			},
		},
		{
			name: "enable-ip",
			env:  map[string]string{envClientIPHeaderDisabled: "false"},
			cfg: config{
				queryString:       true,
				queryStringRegexp: defaultQueryStringRegexp,
				clientIP:          true,
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

func TestIPCollection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		collectIP bool
	}{
		{
			name:      "default-env",
			collectIP: false,
		},
		{
			name:      "disabled-collection",
			env:       map[string]string{envClientIPHeaderDisabled: "true"},
			collectIP: false,
		},
		{
			name:      "enabled-collection",
			env:       map[string]string{envClientIPHeaderDisabled: "false"},
			collectIP: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c := newConfig()
			require.Equal(t, tc.collectIP, c.clientIP)
		})
	}
}

func TestIPCollectionAppSec(t *testing.T) {
	// Make sure that enabling ASM is possible to test some more IP collection cases
	available := func() bool {
		t.Setenv("DD_APPSEC_ENABLED", "1")
		appsec.Start()
		defer appsec.Stop()
		return appsec.Enabled()
	}()
	if !available {
		t.Skip("appsec needs to be available for this test")
	}

	for _, tc := range []struct {
		name      string
		env       map[string]string
		collectIP bool
	}{
		{
			name:      "enabled-appsec",
			env:       map[string]string{"DD_APPSEC_ENABLED": "1"},
			collectIP: true,
		},
		{
			name: "disabled-appsec",
			env:  map[string]string{"DD_APPSEC_ENABLED": "0"},
		},
		{
			name:      "enabled-appsec-enabled-collection",
			env:       map[string]string{"DD_APPSEC_ENABLED": "1", envClientIPHeaderDisabled: "false"},
			collectIP: true,
		},
		{
			name: "enabled-appsec-disabled-collection",
			env:  map[string]string{"DD_APPSEC_ENABLED": "1", envClientIPHeaderDisabled: "true"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			appsec.Start()
			defer appsec.Stop()
			c := newConfig()
			require.Equal(t, tc.collectIP, c.clientIP)
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
