// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildManagedAgentlessEndpoint(t *testing.T) {
	for name, tt := range map[string]struct {
		site    string
		env     string
		apiKey  string
		wantURL string
		wantErr error
	}{
		"default site": {
			apiKey:  "key",
			wantURL: "https://ufc-server.ff-cdn.datadoghq.com/api/v2/feature-flagging/config/rules-based/server",
		},
		"blank site": {
			site:    "  ",
			apiKey:  "key",
			wantURL: "https://ufc-server.ff-cdn.datadoghq.com/api/v2/feature-flagging/config/rules-based/server",
		},
		"lowercased site": {
			site:    "DATADOGHQ.EU",
			apiKey:  "key",
			wantURL: "https://ufc-server.ff-cdn.datadoghq.eu/api/v2/feature-flagging/config/rules-based/server",
		},
		"subdomain site": {
			site:    "us3.datadoghq.com",
			apiKey:  "key",
			wantURL: "https://ufc-server.ff-cdn.us3.datadoghq.com/api/v2/feature-flagging/config/rules-based/server",
		},
		"env is appended": {
			site:    "datadoghq.com",
			env:     "test",
			apiKey:  "key",
			wantURL: "https://ufc-server.ff-cdn.datadoghq.com/api/v2/feature-flagging/config/rules-based/server?dd_env=test",
		},
		"env is URL-escaped": {
			site:    "datadoghq.com",
			env:     "my env",
			apiKey:  "key",
			wantURL: "https://ufc-server.ff-cdn.datadoghq.com/api/v2/feature-flagging/config/rules-based/server?dd_env=my+env",
		},
		"no API key": {
			site:    "datadoghq.com",
			wantErr: errAgentlessNoAPIKey,
		},
		"site with scheme": {
			site:    "https://datadoghq.com",
			apiKey:  "key",
			wantErr: errAgentlessInvalidSite,
		},
		"site with path": {
			site:    "datadoghq.com/path",
			apiKey:  "key",
			wantErr: errAgentlessInvalidSite,
		},
		"site with whitespace": {
			site:    "datadog hq.com",
			apiKey:  "key",
			wantErr: errAgentlessInvalidSite,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ep, err := buildManagedAgentlessEndpoint(tt.site, tt.env, tt.apiKey)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, ep.url)
			assert.True(t, ep.managed)
		})
	}
}

func TestBuildCustomAgentlessEndpoint(t *testing.T) {
	for name, tt := range map[string]struct {
		baseURL string
		wantURL string
		wantErr error
	}{
		"origin only": {
			baseURL: "https://example.com",
			wantURL: "https://example.com/api/v2/feature-flagging/config/rules-based/server",
		},
		"trailing slash": {
			baseURL: "https://example.com/",
			wantURL: "https://example.com/api/v2/feature-flagging/config/rules-based/server",
		},
		"with path": {
			baseURL: "https://example.com/custom/path",
			wantURL: "https://example.com/custom/path",
		},
		"with path and query": {
			baseURL: "https://example.com/api/v2/feature-flagging/config/rules-based/server?dd_env=test",
			wantURL: "https://example.com/api/v2/feature-flagging/config/rules-based/server?dd_env=test",
		},
		"http scheme accepted": {
			baseURL: "http://example.com",
			wantURL: "http://example.com/api/v2/feature-flagging/config/rules-based/server",
		},
		"rejected scheme": {
			baseURL: "ftp://example.com",
			wantErr: errAgentlessScheme,
		},
		"invalid url": {
			baseURL: "://not-a-url",
			wantErr: errAgentlessInvalidURL,
		},
		"empty host": {
			baseURL: "https:///path",
			wantErr: errAgentlessInvalidURL,
		},
		"internal whitespace": {
			baseURL: "https://exa mple.com",
			wantErr: errAgentlessURLWhitespace,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ep, err := buildCustomAgentlessEndpoint(tt.baseURL)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, ep.url)
			assert.False(t, ep.managed)
		})
	}
}

func TestBuildAgentlessEndpointDispatch(t *testing.T) {
	t.Run("blank base URL uses managed endpoint", func(t *testing.T) {
		ep, err := buildAgentlessEndpoint("", "datadoghq.com", "", "key")
		require.NoError(t, err)
		assert.True(t, ep.managed)
	})

	t.Run("non-blank base URL uses custom endpoint", func(t *testing.T) {
		ep, err := buildAgentlessEndpoint("https://example.com", "datadoghq.com", "", "")
		require.NoError(t, err)
		assert.False(t, ep.managed)
	})
}

func TestBuildAgentlessEndpointDoesNotLeakCredentials(t *testing.T) {
	const secret = "s3cr3t-token"

	for name, baseURL := range map[string]string{
		"rejected scheme":     "ftp://user:" + secret + "@example.com",
		"invalid url":         "://user:" + secret + "@[not-a-url",
		"empty host":          "https://user:" + secret + "@",
		"internal whitespace": "https://user:" + secret + "@exa mple.com",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildCustomAgentlessEndpoint(baseURL)
			require.Error(t, err)
			assert.False(t, strings.Contains(err.Error(), secret))
		})
	}
}
