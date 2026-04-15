// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitializeEnvironment_All(t *testing.T) {
	type envCase struct {
		name       string
		preEnv     map[string]string
		wantEnvVal map[string]string
	}

	cases := []envCase{
		{
			name:       "defaults",
			preEnv:     nil,
			wantEnvVal: nil, // will use the default values
		},
		{
			name: "existing preserved",
			preEnv: map[string]string{
				"DD_APM_TRACING_ENABLED": "true",
				"DD_APPSEC_WAF_TIMEOUT":  "5ms",
			},
			wantEnvVal: map[string]string{
				"DD_APM_TRACING_ENABLED": "true",
				"DD_APPSEC_WAF_TIMEOUT":  "5ms",
			},
		},
	}

	var allKeys []string
	for k := range getDefaultEnvVars() {
		allKeys = append(allKeys, k)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(allKeys...)
			setEnv(tc.preEnv)

			initializeEnvironment()

			expected := tc.wantEnvVal
			if expected == nil {
				expected = getDefaultEnvVars()
			}

			for k, want := range expected {
				assert.Equal(t, want, os.Getenv(k), "%s should match", k)
			}
		})
	}
}

func TestConfigureObservabilityMode_All(t *testing.T) {
	unsetEnv("_DD_APPSEC_BLOCKING_UNAVAILABLE")
	assert.NoError(t, configureObservabilityMode(false))
	assert.Equal(t, "", os.Getenv("_DD_APPSEC_BLOCKING_UNAVAILABLE"))

	unsetEnv("_DD_APPSEC_BLOCKING_UNAVAILABLE")
	assert.NoError(t, configureObservabilityMode(true))
	assert.Equal(t, "true", os.Getenv("_DD_APPSEC_BLOCKING_UNAVAILABLE"))
}

func TestLoadConfig_VariousCases(t *testing.T) {
	type want struct {
		extensionPort        string
		healthcheckPort      string
		extensionHost        string
		extensionSocketPath  string
		observabilityMode    bool
		bodyParsingSizeLimit *int
		tlsEnabled           bool
		tlsCertFile          string
		tlsKeyFile           string
	}

	cases := []struct {
		name string
		env  map[string]string
		want want
	}{
		{
			name: "defaults",
			env:  nil,
			want: want{extensionPort: "443", healthcheckPort: "80", extensionHost: "0.0.0.0", tlsEnabled: true, tlsCertFile: "localhost.crt", tlsKeyFile: "localhost.key"},
		},
		{
			name: "valid overrides",
			env: map[string]string{
				"DD_SERVICE_EXTENSION_PORT":               "1234",
				"DD_SERVICE_EXTENSION_HEALTHCHECK_PORT":   "4321",
				"DD_SERVICE_EXTENSION_HOST":               "127.0.0.1",
				"DD_SERVICE_EXTENSION_OBSERVABILITY_MODE": "true",
				"DD_APPSEC_BODY_PARSING_SIZE_LIMIT":       "100000000",
			},
			want: want{extensionPort: "1234", healthcheckPort: "4321", extensionHost: "127.0.0.1", observabilityMode: true, bodyParsingSizeLimit: intPtr(100000000), tlsEnabled: true, tlsCertFile: "localhost.crt", tlsKeyFile: "localhost.key"},
		},
		{
			name: "bad values fall back",
			env: map[string]string{
				"DD_SERVICE_EXTENSION_PORT":               "badport",
				"DD_SERVICE_EXTENSION_HEALTHCHECK_PORT":   "gopher",
				"DD_SERVICE_EXTENSION_OBSERVABILITY_MODE": "notabool",
				"DD_APPSEC_BODY_PARSING_SIZE_LIMIT":       "notanint",
				"DD_SERVICE_EXTENSION_HOST":               "notanip",
			},
			want: want{extensionPort: "443", healthcheckPort: "80", extensionHost: "0.0.0.0", tlsEnabled: true, tlsCertFile: "localhost.crt", tlsKeyFile: "localhost.key"},
		},
		{
			name: "no-tls",
			env: map[string]string{
				"DD_SERVICE_EXTENSION_TLS": "false",
			},
			want: want{extensionPort: "443", healthcheckPort: "80", extensionHost: "0.0.0.0", tlsEnabled: false},
		},
		{
			name: "custom-tls",
			env: map[string]string{
				"DD_SERVICE_EXTENSION_TLS_KEY_FILE":  "/tls/tls.key",
				"DD_SERVICE_EXTENSION_TLS_CERT_FILE": "/tls/tls.crt",
			},
			want: want{extensionPort: "443", healthcheckPort: "80", extensionHost: "0.0.0.0", tlsEnabled: true, tlsCertFile: "/tls/tls.crt", tlsKeyFile: "/tls/tls.key"},
		},
		{
			name: "unix domain socket",
			env: map[string]string{
				"DD_SERVICE_EXTENSION_UDS_PATH": "/var/run/dd-se.sock",
			},
			want: want{extensionPort: "443", healthcheckPort: "80", extensionHost: "0.0.0.0", extensionSocketPath: "/var/run/dd-se.sock", tlsEnabled: true, tlsCertFile: "localhost.crt", tlsKeyFile: "localhost.key"},
		},
	}

	allKeys := []string{
		"DD_SERVICE_EXTENSION_PORT",
		"DD_SERVICE_EXTENSION_HEALTHCHECK_PORT",
		"DD_SERVICE_EXTENSION_HOST",
		"DD_SERVICE_EXTENSION_OBSERVABILITY_MODE",
		"DD_APPSEC_BODY_PARSING_SIZE_LIMIT",
		"DD_SERVICE_EXTENSION_TLS_CERT_FILE",
		"DD_SERVICE_EXTENSION_TLS_KEY_FILE",
		"DD_SERVICE_EXTENSION_TLS",
		"DD_SERVICE_EXTENSION_UDS_PATH",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(allKeys...)
			setEnv(tc.env)

			cfg := loadConfig()
			assert.Equal(t, tc.want.extensionPort, cfg.extensionPort, "extensionPort")
			assert.Equal(t, tc.want.healthcheckPort, cfg.healthcheckPort, "healthcheckPort")
			assert.Equal(t, tc.want.extensionHost, cfg.extensionHost, "extensionHost")
			assert.Equal(t, tc.want.extensionSocketPath, cfg.extensionSocketPath, "extensionSocketPath")
			assert.Equal(t, tc.want.observabilityMode, cfg.observabilityMode, "observabilityMode")
			assert.Equal(t, tc.want.bodyParsingSizeLimit, cfg.bodyParsingSizeLimit, "bodyParsingSizeLimit")

			assert.Equal(t, tc.want.tlsEnabled, cfg.tls != nil, "tlsEnabled")
			if cfg.tls != nil {
				assert.Equal(t, tc.want.tlsCertFile, cfg.tls.certFile, "tlsCertFile")
				assert.Equal(t, tc.want.tlsKeyFile, cfg.tls.keyFile, "tlsKeyFile")
			}
		})
	}
}

func intPtr(v int) *int {
	return &v
}

// Helpers
func unsetEnv(keys ...string) {
	for _, k := range keys {
		err := os.Unsetenv(k)
		if err != nil {
			panic(err)
		}
	}
}

func setEnv(env map[string]string) {
	for k, v := range env {
		err := os.Setenv(k, v)
		if err != nil {
			panic(err)
		}
	}
}
